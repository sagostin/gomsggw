package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	ffmpeg "github.com/u2takey/ffmpeg-go"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
)

// Size limits
const (
	maxImageSize = 1 * 1024 * 1024 // 1 MB
	maxFileSize  = 5 * 1024 * 1024 // 5 MB
)

// MsgFile represents an individual file extracted from the MIME multipart message.
type MsgFile struct {
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Content     []byte `json:"content,omitempty"`
	Base64Data  string `json:"base64_data,omitempty"`
}

func (s *MM4Server) transcodeMedia() {
	lm := s.gateway.LogManager

	for mm4Message := range s.MediaTranscodeChan {
		start := time.Now()

		// Wrap per-message processing in a recover so one bad MMS doesn't kill the goroutine
		func(msg *MM4Message) {
			defer func() {
				if r := recover(); r != nil {
					var clientUser string
					if msg.Client != nil {
						clientUser = msg.Client.Username
					}
					lm.SendLog(lm.BuildLog(
						"Server.MM4.TranscodeMedia",
						"PanicRecovered",
						logrus.ErrorLevel,
						map[string]interface{}{
							"from":          msg.From,
							"to":            msg.To,
							"transactionID": msg.TransactionID,
							"client":        clientUser,
						}, fmt.Errorf("panic: %v", r),
					))
				}
			}()

			var clientUser string
			if msg.Client != nil {
				clientUser = msg.Client.Username
			}

			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"StartTranscode",
				logrus.InfoLevel,
				map[string]interface{}{
					"from":          msg.From,
					"to":            msg.To,
					"transactionID": msg.TransactionID,
					"fileCount":     len(msg.Files),
					"client":        clientUser,
				},
			))

			ff, err := msg.processAndConvertFiles()
			if err != nil {
				// Strip raw content to avoid logging body
				msg.Content = nil

				if msg.Client != nil {
					msg.Client.Password = "***"
				}

				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"TranscodeFailed",
					logrus.ErrorLevel,
					map[string]interface{}{
						"from":          msg.From,
						"to":            msg.To,
						"transactionID": msg.TransactionID,
						"client":        clientUser,
						"fileCount":     len(msg.Files),
					}, err,
				))

				errText := "An error occurred. Please try again later or contact our support if the issue persists. ID: " + msg.TransactionID

				msgItem := &MsgQueueItem{
					To:              msg.From,
					From:            msg.To,
					Type:            "sms",
					message:         errText,
					SkipNumberCheck: false,
					LogID:           msg.TransactionID,
					Delivery: &MsgQueueDelivery{
						Error:      "discard after first attempt",
						RetryTime:  time.Now(),
						RetryCount: 666,
					},
				}

				s.gateway.Router.CarrierMsgChan <- *msgItem
				return
			}

			totalBytes := 0
			for _, f := range ff {
				totalBytes += len(f.Content)
			}

			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"TranscodeSuccess",
				logrus.InfoLevel,
				map[string]interface{}{
					"from":          msg.From,
					"to":            msg.To,
					"transactionID": msg.TransactionID,
					"client":        clientUser,
					"fileCountIn":   len(msg.Files),
					"fileCountOut":  len(ff),
					"totalBytes":    totalBytes,
					"duration_ms":   time.Since(start).Milliseconds(),
				},
			))

			msgItem := MsgQueueItem{
				To:                msg.To,
				From:              msg.From,
				ReceivedTimestamp: time.Now(),
				Type:              MsgQueueItemType.MMS,
				files:             ff,
				LogID:             msg.TransactionID,
			}

			s.gateway.Router.ClientMsgChan <- msgItem
		}(mm4Message)
	}
}

// List of compatible MIME types (currently not enforced, but can be used for validation if needed).
var compatibleTypes = map[string]bool{
	"image/jpeg": true, "image/jpg": true, "image/gif": true, "image/png": true,
	"audio/basic": true, "audio/L24": true, "audio/mp4": true, "audio/mpeg": true,
	"audio/ogg": true, "audio/vnd.rn-realaudio": true, "audio/vnd.wave": true,
	"audio/3gpp": true, "audio/3gpp2": true, "audio/ac3": true, "audio/webm": true,
	"audio/amr-nb": true, "audio/amr": true, "audio/aac": true, "audio/ogg; codecs=opus": true,
	"video/mpeg": true, "video/mp4": true, "video/quicktime": true, "video/webm": true,
	"video/3gpp": true, "video/3gpp2": true, "video/H264": true,
	"application/pdf": true, "application/msword": true,
	"application/vnd.ms-excel": true, "application/vnd.ms-powerpoint": true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
}

func (m *MM4Message) processAndConvertFiles() ([]MsgFile, error) {
	var processedFiles []MsgFile

	for _, file := range m.Files {
		// Pass-through SMIL
		if strings.Contains(file.ContentType, "application/smil") {
			processedFiles = append(processedFiles, file)
			continue
		}

		// Try to decode as base64. If that fails, treat as raw bytes.
		decodedContent, err := decodeBase64(file.Content)
		if err != nil {
			decodedContent = file.Content
		}

		if strings.Contains(file.ContentType, "application/octet-stream") || file.ContentType == "" {
			file.ContentType = detectMIMEType(decodedContent)
		}

		var (
			convertedContent []byte
			newType          string
			newExt           string
		)

		switch {
		case strings.HasPrefix(file.ContentType, "image/"):
			if strings.Contains(file.ContentType, "jpeg") || strings.Contains(file.ContentType, "jpg") {
				convertedContent, err = compressJPEG(decodedContent, int(maxImageSize))
				newType = "image/jpeg"
				newExt = ".jpg"
			} else if strings.Contains(file.ContentType, "png") {
				convertedContent, err = compressPNG(decodedContent, int(maxImageSize))
				newType = "image/png"
				newExt = ".png"
			} else {
				convertedContent, newType, err = convertImageToPNG(decodedContent)
				newExt = ".png"
				if err == nil {
					convertedContent, err = compressPNG(convertedContent, int(maxImageSize))
				}
			}
			if err != nil {
				return nil, fmt.Errorf("failed to process image (%s): %v", file.ContentType, err)
			}

		case strings.HasPrefix(file.ContentType, "video/"):
			convertedContent, newType, err = processVideoContent(decodedContent)
			newExt = ".3gp"
			if err != nil {
				return nil, fmt.Errorf("failed to process video (%s): %v", file.ContentType, err)
			}

		case strings.HasPrefix(file.ContentType, "audio/"):
			convertedContent, newType, err = convertToMP3(decodedContent)
			newExt = ".mp3"
			if err != nil {
				return nil, fmt.Errorf("failed to convert audio (%s): %v", file.ContentType, err)
			}

		default:
			// For non-media files, just enforce size limit. No magical ffmpeg on random docs.
			convertedContent, err = compressFile(decodedContent, int(maxFileSize))
			if err != nil {
				return nil, fmt.Errorf("failed to process file (%s): %v", file.ContentType, err)
			}
			newType = file.ContentType
			newExt = filepath.Ext(file.Filename)
		}

		file.Filename = uuid.New().String() + newExt
		file.Content = convertedContent
		file.ContentType = newType
		file.Base64Data = encodeToBase64(convertedContent)

		processedFiles = append(processedFiles, file)
	}

	return processedFiles, nil
}

// convertTo3GPP compresses and converts video content to 3GPP format suitable for MMS transmission.
func convertTo3GPP(content []byte, transcodeVideo, transcodeAudio bool) ([]byte, error) {
	tempPath := os.Getenv("TRANSCODE_TEMP_PATH")
	if tempPath == "" {
		tempPath = os.TempDir()
	}

	inputFile := filepath.Join(tempPath, uuid.New().String())
	outputFile := filepath.Join(tempPath, uuid.New().String()) // 3GPP container

	if err := ioutil.WriteFile(inputFile, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temporary input file: %v", err)
	}
	defer os.Remove(inputFile)

	ffmpegCmd := ffmpeg.Input(inputFile)
	if transcodeVideo {
		ffmpegCmd = ffmpegCmd.Filter("scale",
			ffmpeg.Args{"w=176", "h=144", "force_original_aspect_ratio=decrease"}).
			Filter("pad",
				ffmpeg.Args{"w=176", "h=144", "x=(ow-iw)/2", "y=(oh-ih)/2"},
			)
	}

	outputArgs := ffmpeg.KwArgs{
		"f": "3gp",
	}

	if transcodeVideo {
		outputArgs["c:v"] = "h263"
		outputArgs["b:v"] = "128k"
		outputArgs["maxrate"] = "128k"
		outputArgs["bufsize"] = "256k"
		outputArgs["r"] = "12"
	} else {
		outputArgs["c:v"] = "copy"
	}

	if transcodeAudio {
		outputArgs["c:a"] = "amr_nb"
		outputArgs["b:a"] = "12.2k"
		outputArgs["ar"] = "8000"
	} else {
		outputArgs["c:a"] = "copy"
	}

	ffmpegCmd = ffmpegCmd.Output(outputFile, outputArgs)

	var stderr bytes.Buffer
	err := ffmpegCmd.OverWriteOutput().ErrorToStdOut().WithErrorOutput(&stderr).Run()
	if err != nil {
		return nil, fmt.Errorf("FFmpeg processing failed: %v\nFFmpeg stderr:\n%s", err, stderr.String())
	}
	defer os.Remove(outputFile)

	processedContent, err := ioutil.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read temporary output file: %v", err)
	}

	if len(processedContent) > int(maxFileSize) {
		return nil, fmt.Errorf("compressed video file exceeds size limit of %.2f KB", float64(maxFileSize)/1024)
	}

	return processedContent, nil
}

// detectMIMEType detects the actual MIME type of the content.
func detectMIMEType(content []byte) string {
	mimeType := mimetype.Detect(content)
	if mimeType != nil {
		return mimeType.String()
	}
	return "application/octet-stream"
}

// convertImageToPNG converts an image to PNG format.
func convertImageToPNG(content []byte) ([]byte, string, error) {
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %v", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", fmt.Errorf("failed to encode image as PNG: %v", err)
	}

	return buf.Bytes(), "image/png", nil
}

// processVideoContent converts video content if needed.
func processVideoContent(content []byte) ([]byte, string, error) {
	videoCodec, audioCodec, err := detectCodecs(content)
	if err != nil {
		// If probing fails, just try transcoding anyway.
		data, err := convertTo3GPP(content, true, true)
		return data, "video/3gpp", err
	}

	// For now we force transcode to 3GPP-compatible regardless, but this is where
	// you could be smarter based on codecs.
	_ = videoCodec
	_ = audioCodec

	data, err := convertTo3GPP(content, true, true)
	return data, "video/3gpp", err
}

// compressJPEG compresses JPEG images to be under maxSize.
func compressJPEG(content []byte, maxSize int) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to decode JPEG: %v", err)
	}

	var buf bytes.Buffer
	quality := 80
	for {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("failed to encode JPEG: %v", err)
		}
		if buf.Len() <= maxSize || quality < 10 {
			break
		}
		quality -= 10
	}

	if buf.Len() > maxSize {
		return nil, fmt.Errorf("JPEG image exceeds size limit after compression")
	}

	return buf.Bytes(), nil
}

// compressPNG compresses PNG images to be under maxSize (re-encode, then enforce limit).
func compressPNG(content []byte, maxSize int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %v", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %v", err)
	}

	if buf.Len() > maxSize {
		return nil, fmt.Errorf("PNG image exceeds size limit")
	}

	return buf.Bytes(), nil
}

// compressFile enforces a max size for non-media files (no actual recompression).
func compressFile(content []byte, maxSize int) ([]byte, error) {
	if len(content) <= maxSize {
		return content, nil
	}
	return nil, fmt.Errorf("file exceeds size limit of %d bytes", maxSize)
}

// detectCodecs probes the input content to determine its codecs.
func detectCodecs(content []byte) (string, string, error) {
	tmpFile, err := ioutil.TempFile("", "probe-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(content); err != nil {
		return "", "", err
	}
	tmpFile.Close()

	data, err := ffmpeg.Probe(tmpFile.Name())
	if err != nil {
		return "", "", err
	}

	type StreamInfo struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}

	var info StreamInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return "", "", err
	}

	var videoCodec, audioCodec string
	for _, stream := range info.Streams {
		if stream.CodecType == "video" {
			videoCodec = stream.CodecName
		} else if stream.CodecType == "audio" {
			audioCodec = stream.CodecName
		}
	}

	return videoCodec, audioCodec, nil
}

// convertToMP4 compresses and converts video content to MP4 format using ffmpeg.
// (Currently unused in the MM4 flow, but left here cleaned up for future use.)
func convertToMP4(content []byte, transcodeVideo, transcodeAudio bool) ([]byte, error) {
	pr, pw := io.Pipe()
	prOut, pwOut := io.Pipe()

	go func() {
		_, _ = pw.Write(content)
		_ = pw.Close()
	}()

	var outputBuffer bytes.Buffer

	args := ffmpeg.KwArgs{}
	if transcodeVideo {
		args["c:v"] = "libx264"
		args["crf"] = 30
		args["preset"] = "veryfast"
		args["maxrate"] = "1M"
		args["bufsize"] = "2M"
	} else {
		args["c:v"] = "copy"
	}

	if transcodeAudio {
		args["c:a"] = "aac"
		args["b:a"] = "96k"
	} else {
		args["c:a"] = "copy"
	}

	go func() {
		err := ffmpeg.Input("pipe:0").
			Output("pipe:1", args).
			WithInput(pr).
			WithOutput(pwOut).
			OverWriteOutput().
			Run()
		if err != nil {
			fmt.Printf("FFmpeg MP4 error: %v\n", err)
		}
		_ = pwOut.Close()
	}()

	_, _ = io.Copy(&outputBuffer, prOut)

	if outputBuffer.Len() > maxFileSize {
		return nil, fmt.Errorf("compressed video file exceeds size limit of %d bytes", maxFileSize)
	}

	return outputBuffer.Bytes(), nil
}

// convertToMP3 compresses and converts audio content to MP3 format using ffmpeg.
func convertToMP3(content []byte) ([]byte, string, error) {
	pr, pw := io.Pipe()
	prOut, pwOut := io.Pipe()

	go func() {
		_, _ = pw.Write(content)
		_ = pw.Close()
	}()

	var outputBuffer bytes.Buffer

	go func() {
		err := ffmpeg.Input("pipe:0").
			Output("pipe:1", ffmpeg.KwArgs{
				"c:a": "libmp3lame",
				"b:a": "128k",
				"ar":  "44100",
			}).
			WithInput(pr).
			WithOutput(pwOut).
			OverWriteOutput().
			Run()
		if err != nil {
			fmt.Printf("FFmpeg MP3 error: %v\n", err)
		}
		_ = pwOut.Close()
	}()

	_, _ = io.Copy(&outputBuffer, prOut)

	if outputBuffer.Len() > maxFileSize {
		return nil, "", fmt.Errorf("compressed audio file exceeds size limit of 5MB")
	}

	return outputBuffer.Bytes(), "audio/mp3", nil
}

// encodeToBase64 converts raw bytes to Base64.
func encodeToBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// decodeBase64 decodes a Base64 string.
func decodeBase64(encodedContent []byte) ([]byte, error) {
	return base64.StdEncoding.DecodeString(string(encodedContent))
}

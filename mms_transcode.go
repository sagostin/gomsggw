package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	ffmpeg "github.com/u2takey/ffmpeg-go"

	_ "image/gif"
	_ "image/jpeg"
)

// MMS Size Limits
// These can be adjusted based on carrier requirements
const (
	// Target size for transcoded output (images/videos that we CAN compress)
	// This is the goal we try to achieve through compression
	targetOutputSize = 600 * 1024 // 600 KB - safe for most carriers

	// Maximum input size we'll attempt to process
	maxInputSize = 10 * 1024 * 1024 // 10 MB

	// Per-type limits for files we CAN'T transcode (passthrough limits)
	maxGIFSize   = 600 * 1024      // 600 KB - animated GIFs can't be resized
	maxAudioSize = 1 * 1024 * 1024 // 1 MB - audio files
	maxVideoSize = 1 * 1024 * 1024 // 1 MB - video output after transcoding
	maxOtherSize = 600 * 1024      // 600 KB - other file types (PDFs, docs, etc.)

	// Legacy aliases
	maxImageSize = targetOutputSize
	maxFileSize  = targetOutputSize
)

// TranscodeError provides user-friendly error messages for MMS transcoding failures
type TranscodeError struct {
	Code        string // Machine-readable error code
	UserMessage string // User-friendly message to send back to sender
	Details     string // Technical details for logging
}

func (e TranscodeError) Error() string {
	return e.Details
}

// Common transcoding errors with user-friendly messages
var (
	ErrFileTooLarge = TranscodeError{
		Code:        "FILE_TOO_LARGE",
		UserMessage: "Your file is too large to process (max 10MB). Please reduce the file size and try again.",
		Details:     "input file exceeds maximum allowed size of 10MB",
	}
	ErrGIFTooLarge = TranscodeError{
		Code:        "GIF_TOO_LARGE",
		UserMessage: "Animated GIFs cannot be resized. Please send a smaller GIF (max 600KB).",
		Details:     "animated GIF exceeds size limit and cannot be transcoded",
	}
	ErrCompressionFailed = TranscodeError{
		Code:        "COMPRESSION_FAILED",
		UserMessage: "Your media couldn't be compressed enough for MMS. Please try a smaller file.",
		Details:     "failed to compress media to target size",
	}
	ErrUnsupportedFormat = TranscodeError{
		Code:        "UNSUPPORTED_FORMAT",
		UserMessage: "This file type is not supported for MMS. Supported formats: JPEG, PNG, GIF, MP4, 3GP.",
		Details:     "unsupported media format for MMS",
	}
)

// MsgFile represents an individual file extracted from the MIME multipart message.
type MsgFile struct {
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Content     []byte `json:"content,omitempty"`
	Base64Data  string `json:"base64_data,omitempty"`
	MediaURL    string `json:"media_url,omitempty"` // URL to fetch media from (Bicom/Telnyx)
}

// safeClientInfo builds a log field map from an MM4Message without panicking.
func safeClientInfo(m *MM4Message) map[string]interface{} {
	fields := map[string]interface{}{
		"transaction_id": m.TransactionID,
		"message_id":     m.MessageID,
		"from":           m.From,
		"to":             m.To,
	}
	if m.Client != nil {
		fields["client"] = m.Client.Username
	}
	return fields
}

func (s *MM4Server) transcodeMedia() {
	lm := s.gateway.LogManager

	for {
		mm4Message := <-s.MediaTranscodeChan

		start := time.Now()
		baseFields := safeClientInfo(mm4Message)
		baseFields["file_count"] = len(mm4Message.Files)

		lm.SendLog(lm.BuildLog(
			"Server.MM4.TranscodeMedia",
			"TranscodeStart",
			logrus.DebugLevel,
			baseFields,
		))

		// Per-file summary before processing
		for i, f := range mm4Message.Files {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"InputFileSummary",
				logrus.DebugLevel,
				map[string]interface{}{
					"transaction_id": mm4Message.TransactionID,
					"index":          i,
					"filename":       f.Filename,
					"content_type":   f.ContentType,
					"size_bytes":     len(f.Content),
				},
			))
		}

		// Panic guard around media processing
		func() {
			defer func() {
				if r := recover(); r != nil {
					lm.SendLog(lm.BuildLog(
						"Server.MM4.TranscodeMedia",
						"PanicRecovered",
						logrus.ErrorLevel,
						map[string]interface{}{
							"transaction_id": mm4Message.TransactionID,
							"panic":          fmt.Sprintf("%v", r),
							"duration_ms":    time.Since(start).Milliseconds(),
						},
					))

					msg := &MsgQueueItem{
						To:              mm4Message.From,
						From:            mm4Message.To,
						Type:            "sms",
						message:         "An internal error occurred while processing your media. Please try again later. ID: " + mm4Message.TransactionID,
						SkipNumberCheck: false,
						LogID:           mm4Message.TransactionID,
						Delivery: &MsgQueueDelivery{
							Error:      "discard after first attempt (panic)",
							RetryTime:  time.Now(),
							RetryCount: 666,
						},
					}
					s.gateway.Router.CarrierMsgChan <- *msg
				}
			}()

			ff, err := mm4Message.processAndConvertFiles(lm)
			if err != nil {
				// scrub large / sensitive stuff before logging
				mm4Message.Files = nil
				mm4Message.Content = nil
				if mm4Message.Client != nil {
					mm4Message.Client.Password = "***"
				}

				errFields := safeClientInfo(mm4Message)
				errFields["logID"] = mm4Message.TransactionID

				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"TranscodeError",
					logrus.ErrorLevel,
					errFields,
					err,
				))

				// Use user-friendly message from TranscodeError if available
				userMsg := "An error occurred. Please try again later or contact support. ID: " + mm4Message.TransactionID
				if te, ok := err.(TranscodeError); ok {
					userMsg = te.UserMessage + " ID: " + mm4Message.TransactionID
					errFields["error_code"] = te.Code
				}

				msg := &MsgQueueItem{
					To:              mm4Message.From,
					From:            mm4Message.To,
					Type:            "sms",
					message:         userMsg,
					SkipNumberCheck: false,
					LogID:           mm4Message.TransactionID,
					Delivery: &MsgQueueDelivery{
						Error:      "discard after first attempt",
						RetryTime:  time.Now(),
						RetryCount: 666,
					},
				}

				s.gateway.Router.CarrierMsgChan <- *msg
				return
			}

			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"TranscodeSuccess",
				logrus.InfoLevel,
				map[string]interface{}{
					"transaction_id": mm4Message.TransactionID,
					"output_files":   len(ff),
					"duration_ms":    time.Since(start).Milliseconds(),
				},
			))

			for i, f := range ff {
				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"OutputFileSummary",
					logrus.DebugLevel,
					map[string]interface{}{
						"transaction_id": mm4Message.TransactionID,
						"index":          i,
						"filename":       f.Filename,
						"content_type":   f.ContentType,
						"size_bytes":     len(f.Content),
					},
				))
			}

			// Calculate original file sizes before transcoding
			var originalSizeBytes int
			for _, f := range mm4Message.Files {
				originalSizeBytes += len(f.Content)
			}

			msgItem := MsgQueueItem{
				To:                mm4Message.To,
				From:              mm4Message.From,
				ReceivedTimestamp: time.Now(),
				Type:              MsgQueueItemType.MMS,
				files:             ff,
				LogID:             mm4Message.TransactionID,
				OriginalSizeBytes: originalSizeBytes,
			}

			s.gateway.Router.ClientMsgChan <- msgItem
		}()
	}
}

// List of compatible MIME types
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

// NOTE: requires a *LogManager so we can log without using logrus directly.
func (m *MM4Message) processAndConvertFiles(lm *LogManager) ([]MsgFile, error) {
	var processedFiles []MsgFile

	baseFields := safeClientInfo(m)

	for idx, file := range m.Files {
		start := time.Now()

		entryFields := map[string]interface{}{
			"transaction_id": m.TransactionID,
			"index":          idx,
			"filename_in":    file.Filename,
			"content_type":   file.ContentType,
			"raw_size":       len(file.Content),
			"is_smil":        strings.Contains(file.ContentType, "application/smil"),
			"has_base64":     len(file.Base64Data) > 0,
		}
		for k, v := range baseFields {
			entryFields[k] = v
		}

		if strings.Contains(file.ContentType, "application/smil") {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileSkipSMIL",
				logrus.DebugLevel,
				entryFields,
			))
			processedFiles = append(processedFiles, file)
			continue
		}

		lm.SendLog(lm.BuildLog(
			"Server.MM4.TranscodeMedia",
			"FileDecodeBase64Start",
			logrus.DebugLevel,
			entryFields,
		))

		decodedContent, err := decodeBase64(file.Content)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileDecodeBase64Error",
				logrus.ErrorLevel,
				entryFields,
				err,
			))
			return nil, fmt.Errorf("failed to decode Base64 content: %v", err)
		}

		entryFields["decoded_size"] = len(decodedContent)

		// Check input size limit (10MB max)
		if len(decodedContent) > maxInputSize {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileInputTooLarge",
				logrus.WarnLevel,
				entryFields,
			))
			return nil, ErrFileTooLarge
		}

		// Check for animated GIF (cannot be resized per Telnyx docs)
		if strings.Contains(file.ContentType, "image/gif") {
			if isAnimatedGIF(decodedContent) {
				entryFields["is_animated_gif"] = true
				if len(decodedContent) > int(targetOutputSize) {
					lm.SendLog(lm.BuildLog(
						"Server.MM4.TranscodeMedia",
						"AnimatedGIFTooLarge",
						logrus.WarnLevel,
						entryFields,
					))
					return nil, ErrGIFTooLarge
				}
				// Animated GIF is small enough, pass through unchanged
				file.Base64Data = encodeToBase64(decodedContent)
				file.Content = decodedContent
				processedFiles = append(processedFiles, file)
				continue
			}
		}

		if strings.Contains(file.ContentType, "application/octet-stream") || file.ContentType == "" {
			detected := detectMIMEType(decodedContent)
			entryFields["detected_mime"] = detected
			file.ContentType = detected

			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileMimeDetected",
				logrus.DebugLevel,
				entryFields,
			))
		}

		entryFields["branch_content_type"] = file.ContentType

		var convertedContent []byte
		var newType string
		var newExt string

		switch {
		case strings.HasPrefix(file.ContentType, "image/"):
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileProcessImageStart",
				logrus.DebugLevel,
				entryFields,
			))

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
				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"FileProcessImageError",
					logrus.ErrorLevel,
					entryFields,
					err,
				))
				return nil, fmt.Errorf("failed to process image: %v", err)
			}

		case strings.HasPrefix(file.ContentType, "video/"):
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileProcessVideoStart",
				logrus.DebugLevel,
				entryFields,
			))

			convertedContent, newType, err = processVideoContent(decodedContent)
			newExt = ".3gp"
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"FileProcessVideoError",
					logrus.ErrorLevel,
					entryFields,
					err,
				))
				return nil, fmt.Errorf("failed to process video: %v", err)
			}

		case strings.HasPrefix(file.ContentType, "audio/"):
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileProcessAudioStart",
				logrus.DebugLevel,
				entryFields,
			))

			convertedContent, newType, err = convertToMP3(decodedContent)
			newExt = ".mp3"
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"FileProcessAudioError",
					logrus.ErrorLevel,
					entryFields,
					err,
				))
				return nil, fmt.Errorf("failed to convert audio: %v", err)
			}

		default:
			lm.SendLog(lm.BuildLog(
				"Server.MM4.TranscodeMedia",
				"FileProcessGenericStart",
				logrus.DebugLevel,
				entryFields,
			))

			convertedContent, err = compressFile(decodedContent, int(maxFileSize))
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Server.MM4.TranscodeMedia",
					"FileProcessGenericError",
					logrus.ErrorLevel,
					entryFields,
					err,
				))
				return nil, fmt.Errorf("failed to compress file: %v", err)
			}
			newType = file.ContentType
			newExt = filepath.Ext(file.Filename)
		}

		file.Filename = uuid.New().String() + newExt
		file.Content = convertedContent
		file.ContentType = newType
		file.Base64Data = encodeToBase64(convertedContent)

		entryFields["filename_out"] = file.Filename
		entryFields["final_type"] = file.ContentType
		entryFields["final_size"] = len(file.Content)
		entryFields["duration_ms"] = time.Since(start).Milliseconds()
		entryFields["new_ext"] = newExt
		entryFields["compatibleType"] = compatibleTypes[file.ContentType]

		lm.SendLog(lm.BuildLog(
			"Server.MM4.TranscodeMedia",
			"FileProcessSuccess",
			logrus.InfoLevel,
			entryFields,
		))

		processedFiles = append(processedFiles, file)
	}

	return processedFiles, nil
}

// convertTo3GPP compresses and converts video content to 3GPP format suitable for MMS transmission.
func convertTo3GPP(content []byte, transcodeVideo, transcodeAudio bool) ([]byte, error) {
	// Determine temporary file path
	tempPath := os.Getenv("TRANSCODE_TEMP_PATH")
	if tempPath == "" {
		tempPath = os.TempDir() // Use OS temp directory as fallback
	}

	// Generate unique file names for input and output
	inputFile := filepath.Join(tempPath, uuid.New().String())
	outputFile := filepath.Join(tempPath, uuid.New().String()) // Use .3gp extension for 3GPP format

	// Save the input content to a temporary file
	err := ioutil.WriteFile(inputFile, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write temporary input file: %v", err)
	}
	defer os.Remove(inputFile) // Ensure cleanup

	// Build FFmpeg command
	ffmpegCmd := ffmpeg.Input(inputFile)

	// Apply video filters if transcoding is required
	if transcodeVideo {
		// Apply scale and pad filters to maintain aspect ratio and fit into 176x144
		ffmpegCmd = ffmpegCmd.Filter("scale", ffmpeg.Args{"w=176", "h=144", "force_original_aspect_ratio=decrease"}).Filter(
			"pad",
			ffmpeg.Args{"w=176", "h=144", "x=(ow-iw)/2", "y=(oh-ih)/2"},
		)
	}

	// Prepare output arguments
	outputArgs := ffmpeg.KwArgs{
		"f": "3gp", // Output format
	}

	// Set video codec options
	if transcodeVideo {
		outputArgs["c:v"] = "h263"     // Use H.263 codec for compatibility
		outputArgs["b:v"] = "128k"     // Lower video bitrate for smaller size
		outputArgs["maxrate"] = "128k" // Limit max bitrate
		outputArgs["bufsize"] = "256k" // Buffer size for rate control
		outputArgs["r"] = "12"         // Reduce frame rate to 12 FPS
	} else {
		outputArgs["c:v"] = "copy"
	}

	// Set audio codec options
	if transcodeAudio {
		outputArgs["c:a"] = "amr_nb" // Use AMR-NB codec for MMS compatibility
		outputArgs["b:a"] = "12.2k"  // Lower audio bitrate
		outputArgs["ar"] = "8000"    // Set audio sample rate to 8000 Hz
	} else {
		outputArgs["c:a"] = "copy"
	}

	// Add output to command
	ffmpegCmd = ffmpegCmd.Output(outputFile, outputArgs)

	// Capture FFmpeg's stderr output for debugging
	var stderr bytes.Buffer
	err = ffmpegCmd.OverWriteOutput().ErrorToStdOut().WithErrorOutput(&stderr).Run()
	if err != nil {
		return nil, fmt.Errorf("FFmpeg processing failed: %v\nFFmpeg stderr:\n%s", err, stderr.String())
	}
	defer os.Remove(outputFile) // Ensure cleanup

	// Read the processed output file
	processedContent, err := ioutil.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read temporary output file: %v", err)
	}

	// Validate file size (600 KB limit for MMS)
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

// isAnimatedGIF checks if the GIF content contains multiple frames (animated)
func isAnimatedGIF(content []byte) bool {
	reader := bytes.NewReader(content)

	// Decode the GIF to check frame count
	g, err := gif.DecodeAll(reader)
	if err != nil {
		// If we can't decode it as GIF, assume not animated
		return false
	}

	// Animated GIF has more than one frame
	return len(g.Image) > 1
}

// convertImageToPNG converts an image to PNG format.
func convertImageToPNG(content []byte) ([]byte, string, error) {
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %v", err)
	}

	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode image as PNG: %v", err)
	}

	return buf.Bytes(), "image/png", nil
}

// processVideoContent converts video content if needed.
func processVideoContent(content []byte) ([]byte, string, error) {
	_, _, err := detectCodecs(content)
	if err != nil {
		return nil, "", err
	}

	/*transcodeVideo := videoCodec != "h264"
	transcodeAudio := audioCodec != "aac"

	if !transcodeVideo && !transcodeAudio {
		return content, "video/3gpp", nil
	}*/

	data, err := convertTo3GPP(content, true, false)

	return data, "video/3gpp", err
}

// compressJPEG compresses JPEG images with progressive quality reduction and dimension resizing.
// Targets Tier 2 carrier limit (600KB) for maximum compatibility.
func compressJPEG(content []byte, maxSize int) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to decode JPEG: %v", err)
	}

	// Quality levels to try (aggressive reduction)
	qualityLevels := []int{85, 70, 55, 40, 25, 15, 5}
	// Dimension scale factors to try if quality alone isn't enough
	scaleFactors := []float64{1.0, 0.75, 0.5, 0.35, 0.25}

	var buf bytes.Buffer
	var currentImg image.Image = img

	for _, scale := range scaleFactors {
		if scale < 1.0 {
			// Resize the image
			currentImg = resizeImage(img, scale)
		}

		for _, quality := range qualityLevels {
			buf.Reset()
			err = jpeg.Encode(&buf, currentImg, &jpeg.Options{Quality: quality})
			if err != nil {
				return nil, fmt.Errorf("failed to encode JPEG: %v", err)
			}

			if buf.Len() <= maxSize {
				return buf.Bytes(), nil
			}
		}
	}

	// If we still can't fit, return ErrCompressionFailed
	return nil, ErrCompressionFailed
}

// resizeImage scales an image by the given factor while maintaining aspect ratio
func resizeImage(img image.Image, scale float64) image.Image {
	bounds := img.Bounds()
	newWidth := int(float64(bounds.Dx()) * scale)
	newHeight := int(float64(bounds.Dy()) * scale)

	// Use simple nearest-neighbor resize (good enough for compression purposes)
	newImg := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			srcX := int(float64(x) / scale)
			srcY := int(float64(y) / scale)
			newImg.Set(x, y, img.At(srcX, srcY))
		}
	}

	return newImg
}

// compressPNG attempts PNG compression first, then falls back to JPEG if still too large.
// PNG is lossless and cannot be effectively compressed, so we convert to JPEG when needed.
// Note: This changes the output format from PNG to JPEG when fallback is used.
func compressPNG(content []byte, maxSize int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %v", err)
	}

	// Try PNG first
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	if err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %v", err)
	}

	// If PNG is small enough, return it
	if buf.Len() <= maxSize {
		return buf.Bytes(), nil
	}

	// PNG too large - fall back to JPEG compression with progressive quality/resize
	return compressImageToJPEG(img, maxSize)
}

// compressImageToJPEG compresses an already-decoded image to JPEG with progressive quality/resize
func compressImageToJPEG(img image.Image, maxSize int) ([]byte, error) {
	qualityLevels := []int{85, 70, 55, 40, 25, 15, 5}
	scaleFactors := []float64{1.0, 0.75, 0.5, 0.35, 0.25}

	var buf bytes.Buffer
	var currentImg image.Image = img

	for _, scale := range scaleFactors {
		if scale < 1.0 {
			currentImg = resizeImage(img, scale)
		}

		for _, quality := range qualityLevels {
			buf.Reset()
			err := jpeg.Encode(&buf, currentImg, &jpeg.Options{Quality: quality})
			if err != nil {
				return nil, fmt.Errorf("failed to encode JPEG: %v", err)
			}

			if buf.Len() <= maxSize {
				return buf.Bytes(), nil
			}
		}
	}

	return nil, ErrCompressionFailed
}

// compressFile compresses any other file type to be under the specified max size.
func compressFile(content []byte, maxSize int) ([]byte, error) {
	if len(content) <= maxSize {
		return content, nil
	}

	pr, pw := io.Pipe()
	prOut, pwOut := io.Pipe()
	defer pr.Close()
	defer pwOut.Close()

	go func() {
		_, _ = pw.Write(content)
		_ = pw.Close()
	}()

	var outputBuffer bytes.Buffer
	ffmpegCmd := ffmpeg.Input("pipe:0").
		Output("pipe:1", ffmpeg.KwArgs{"c:v": "libx264", "crf": 28, "preset": "slow"}).
		WithInput(pr).
		WithOutput(pwOut).
		OverWriteOutput().
		Run()

	go func() {
		_ = ffmpegCmd
		_ = pwOut.Close()
	}()

	_, _ = io.Copy(&outputBuffer, prOut)
	if outputBuffer.Len() > maxSize {
		return nil, fmt.Errorf("file exceeds size limit after compression")
	}

	return outputBuffer.Bytes(), nil
}

// detectCodecs probes the input content to determine its codecs.
func detectCodecs(content []byte) (string, string, error) {
	tmpFile, err := ioutil.TempFile("", "probe-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.Write(content)
	if err != nil {
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
	err = json.Unmarshal([]byte(data), &info)
	if err != nil {
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
func convertToMP4(content []byte, transcodeVideo, transcodeAudio bool) ([]byte, error) {
	pr, pw := io.Pipe()
	prOut, pwOut := io.Pipe()

	go func() {
		_, _ = pw.Write(content)
		_ = pw.Close()
	}()

	var outputBuffer bytes.Buffer
	ffmpegCmd := ffmpeg.Input("pipe:0")

	// Set video transcoding options with compression
	if transcodeVideo {
		ffmpegCmd = ffmpegCmd.Output("pipe:1", ffmpeg.KwArgs{
			"c:v":     "libx264",
			"crf":     30, // Higher CRF value for better compression
			"preset":  "veryfast",
			"maxrate": "1M",
			"bufsize": "2M",
		})
	} else {
		ffmpegCmd = ffmpegCmd.Output("pipe:1", ffmpeg.KwArgs{"c:v": "copy"})
	}

	// Set audio transcoding options with compression
	if transcodeAudio {
		ffmpegCmd = ffmpegCmd.Output("pipe:1", ffmpeg.KwArgs{
			"c:a": "aac",
			"b:a": "96k", // Lower bitrate for audio compression
		})
	} else {
		ffmpegCmd = ffmpegCmd.Output("pipe:1", ffmpeg.KwArgs{"c:a": "copy"})
	}

	go func() {
		_ = ffmpegCmd.WithInput(pr).WithOutput(pwOut).OverWriteOutput().Run()
		_ = pwOut.Close()
	}()

	_, _ = io.Copy(&outputBuffer, prOut)

	// Check if the output is larger than the allowed limit (5MB)
	if outputBuffer.Len() > maxFileSize {
		return nil, fmt.Errorf("compressed video file exceeds size limit of 5MB")
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
				"b:a": "128k", // Set bitrate for compression
				"ar":  "44100",
			}).
			WithInput(pr).
			WithOutput(pwOut).
			OverWriteOutput().
			Run()
		if err != nil {
			// error gets returned via caller, no direct logging here
			_ = err
		}
		_ = pwOut.Close()
	}()

	_, _ = io.Copy(&outputBuffer, prOut)

	// Check if the output is larger than the allowed limit (5MB)
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

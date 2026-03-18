package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	ovpBaseURL = "https://api.telus.com/rest/v0/OneVoiceDIDMessaging_vs0/v1"
)

// OneVoicePlusHandler implements CarrierHandler for OneVoicePlus (Telus)
type OneVoicePlusHandler struct {
	BaseCarrierHandler
	gateway  *Gateway
	carrier  *Carrier
	password string // X-TELUS-SDF-Developer-Key
}

// NewOneVoicePlusHandler initializes a new OneVoicePlusHandler
func NewOneVoicePlusHandler(gateway *Gateway, carrier *Carrier, decryptedUsername string, decryptedPassword string) *OneVoicePlusHandler {
	return &OneVoicePlusHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "onevoiceplus"},
		gateway:            gateway,
		carrier:            carrier,
		password:           decryptedPassword,
	}
}

// --- Outbound API request/response types ---

// OVPSMSRequest is the request body for sending an SMS via OneVoicePlus.
type OVPSMSRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	MessageText string `json:"messageText"`
}

// OVPMMSRequest is the request body for sending an MMS via OneVoicePlus.
type OVPMMSRequest struct {
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	MessageText string   `json:"messageText,omitempty"`
	MediaID     []string `json:"mediaId"`
}

// OVPUploadMediaRequest is the request body for uploading media via OneVoicePlus.
type OVPUploadMediaRequest struct {
	ControlPart OVPControlPart `json:"controlPart"`
	MediaPart   OVPMediaPart   `json:"mediaPart"`
}

// OVPControlPart describes the media file metadata.
type OVPControlPart struct {
	FileName string `json:"fileName"`
	FileType string `json:"fileType"` // e.g., "image", "audio", "video"
}

// OVPMediaPart contains the base64-encoded media content.
type OVPMediaPart struct {
	ContentType  string `json:"contentType"`
	MediaContent string `json:"mediaContent"` // base64-encoded
}

// OVPUploadMediaResponse is the response from the Upload Media endpoint.
type OVPUploadMediaResponse struct {
	MediaID string `json:"mediaId"`
}

// --- Inbound callback types ---

// OVPInboundPayload represents the inbound message callback from OneVoicePlus.
type OVPInboundPayload struct {
	MessageID string `json:"messageId"`
	From      string `json:"from"`
	To        string `json:"to"`
	Message   string `json:"message"`
}

// --- CarrierHandler interface implementation ---

// Inbound handles incoming OneVoicePlus webhooks (SMS only per API docs).
func (h *OneVoicePlusHandler) Inbound(c iris.Context) error {
	lm := h.gateway.LogManager

	var payload OVPInboundPayload
	if err := c.ReadBody(&payload); err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.Inbound.OneVoicePlus",
			"GenericError",
			logrus.ErrorLevel,
			nil,
			err,
		))
		c.StatusCode(http.StatusBadRequest)
		return nil
	}

	if payload.From == "" || payload.To == "" {
		lm.SendLog(lm.BuildLog(
			"Carrier.Inbound.OneVoicePlus",
			"MissingFromOrTo",
			logrus.ErrorLevel,
			nil,
		))
		c.StatusCode(http.StatusBadRequest)
		return nil
	}

	logID := primitive.NewObjectID().Hex()

	if strings.TrimSpace(payload.Message) != "" {
		sms := MsgQueueItem{
			To:                payload.To,
			From:              payload.From,
			ReceivedTimestamp: time.Now(),
			Type:              MsgQueueItemType.SMS,
			message:           payload.Message,
			LogID:             logID,
			SourceCarrier:     h.carrier.Name,
		}
		h.gateway.Router.CarrierMsgChan <- sms
	}

	lm.SendLog(lm.BuildLog(
		"Carrier.OneVoicePlus.Inbound",
		"received",
		logrus.InfoLevel,
		map[string]interface{}{
			"logID":     logID,
			"carrierID": payload.MessageID,
			"from":      payload.From,
			"to":        payload.To,
		}, nil,
	))

	c.StatusCode(http.StatusOK)
	return nil
}

// SendSMS sends an SMS message via the OneVoicePlus API.
func (h *OneVoicePlusHandler) SendSMS(sms *MsgQueueItem) (string, error) {
	lm := h.gateway.LogManager

	reqBody := OVPSMSRequest{
		Source:      sms.From,
		Destination: sms.To,
		MessageText: sms.message,
	}

	payloadBytes, err := json.Marshal(reqBody)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.OneVoicePlus",
			"Failed to marshal payload: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	req, err := http.NewRequest("POST", ovpBaseURL+"/sms/outbound", bytes.NewBuffer(payloadBytes))
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.OneVoicePlus",
			"Failed to build request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TELUS-SDF-Developer-Key", h.password)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.OneVoicePlus",
			"Failed to send request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.OneVoicePlus",
			"Failed to read response body: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.OneVoicePlus",
			"Failed to send SMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           sms.LogID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"to":              sms.To,
				"from":            sms.From,
				"response_body":   string(bodyBytes),
			}, nil,
		))
		return "", fmt.Errorf("failed to send SMS via OneVoicePlus (HTTP %d)", resp.StatusCode)
	}

	return "", nil
}

// SendMMS sends an MMS message via the OneVoicePlus API.
// OneVoicePlus requires a two-step process:
// 1. Upload each media file via the Upload Media endpoint to get a mediaId.
// 2. Send the MMS referencing the mediaIds.
func (h *OneVoicePlusHandler) SendMMS(mms *MsgQueueItem) (string, error) {
	lm := h.gateway.LogManager

	var mediaIDs []string

	for _, file := range mms.files {
		if strings.Contains(file.ContentType, "application/smil") {
			continue
		}

		// Determine base64 content — prefer Base64Data if already set,
		// otherwise the raw Content bytes will be encoded by uploadMedia.
		mediaID, err := h.uploadMedia(file, mms.LogID)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Carrier.SendMMS.OneVoicePlus",
				"UploadMediaError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID":    mms.LogID,
					"filename": file.Filename,
				}, err,
			))
			return "", fmt.Errorf("failed to upload media for MMS: %w", err)
		}
		mediaIDs = append(mediaIDs, mediaID)
	}

	if len(mediaIDs) == 0 {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.OneVoicePlus",
			"NoMediaToSend",
			logrus.WarnLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			},
		))
		return "", errors.New("no media files to send via OneVoicePlus MMS")
	}

	reqBody := OVPMMSRequest{
		Source:      mms.From,
		Destination: mms.To,
		MediaID:     mediaIDs,
	}

	payloadBytes, err := json.Marshal(reqBody)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.OneVoicePlus",
			"Failed to marshal MMS payload: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	req, err := http.NewRequest("POST", ovpBaseURL+"/mms/outbound", bytes.NewBuffer(payloadBytes))
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.OneVoicePlus",
			"Failed to build MMS request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TELUS-SDF-Developer-Key", h.password)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.OneVoicePlus",
			"Failed to send MMS request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.OneVoicePlus",
			"Failed to read MMS response body: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.OneVoicePlus",
			"Failed to send MMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           mms.LogID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"to":              mms.To,
				"from":            mms.From,
				"response_body":   string(bodyBytes),
			}, nil,
		))
		return "", fmt.Errorf("failed to send MMS via OneVoicePlus (HTTP %d)", resp.StatusCode)
	}

	return "", nil
}

// --- Private helpers ---

// uploadMedia uploads a single media file to the OneVoicePlus media endpoint
// and returns the assigned mediaId.
func (h *OneVoicePlusHandler) uploadMedia(file MsgFile, logID string) (string, error) {
	lm := h.gateway.LogManager

	// Determine base64 content
	b64Content := file.Base64Data
	if b64Content == "" && len(file.Content) > 0 {
		b64Content = encodeToBase64(file.Content)
	}

	if b64Content == "" {
		return "", errors.New("no media content available for upload")
	}

	// Determine file type category from content type
	fileType := ovpFileTypeFromContentType(file.ContentType)

	reqBody := OVPUploadMediaRequest{
		ControlPart: OVPControlPart{
			FileName: file.Filename,
			FileType: fileType,
		},
		MediaPart: OVPMediaPart{
			ContentType:  "application/octet-stream",
			MediaContent: b64Content,
		},
	}

	payloadBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal upload media request: %w", err)
	}

	req, err := http.NewRequest("POST", ovpBaseURL+"/media/assets", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("failed to build upload media request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TELUS-SDF-Developer-Key", h.password)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload media HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read upload media response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		lm.SendLog(lm.BuildLog(
			"Carrier.UploadMedia.OneVoicePlus",
			"UploadMediaFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           logID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"filename":        file.Filename,
				"response_body":   string(bodyBytes),
			}, nil,
		))
		return "", fmt.Errorf("upload media failed (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var uploadResp OVPUploadMediaResponse
	if err := json.Unmarshal(bodyBytes, &uploadResp); err != nil {
		return "", fmt.Errorf("failed to parse upload media response: %w", err)
	}

	if uploadResp.MediaID == "" {
		return "", errors.New("upload media response did not contain a mediaId")
	}

	return uploadResp.MediaID, nil
}

// ovpFileTypeFromContentType maps a MIME content type to the OneVoicePlus
// file type category used in the Upload Media controlPart.
func ovpFileTypeFromContentType(contentType string) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case strings.HasPrefix(ct, "audio/"):
		return "audio"
	case strings.HasPrefix(ct, "video/"):
		return "video"
	default:
		return "file"
	}
}

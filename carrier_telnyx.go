package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// TelnyxHandler implements CarrierHandler for Telnyx
type TelnyxHandler struct {
	BaseCarrierHandler
	gateway  *Gateway
	carrier  *Carrier
	password string
}

// NewTelnyxHandler initializes a new TelnyxHandler
func NewTelnyxHandler(gateway *Gateway, carrier *Carrier, decryptedUsername string, decryptedPassword string) *TelnyxHandler {
	return &TelnyxHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "telnyx"},
		gateway:            gateway,
		carrier:            carrier,
		password:           decryptedPassword,
	}
}

// TelnyxMessage represents the structure of Telnyx outbound messages
type TelnyxMessage struct {
	From               string   `json:"from"`
	To                 string   `json:"to"`
	Text               string   `json:"text,omitempty"`
	Subject            string   `json:"subject,omitempty"`
	MediaUrls          []string `json:"media_urls,omitempty"`
	MessagingProfileID string   `json:"messaging_profile_id,omitempty"`
	WebhookURL         string   `json:"webhook_url,omitempty"`
	WebhookFailoverURL string   `json:"webhook_failover_url,omitempty"`
}

// TelnyxResponse represents the response from Telnyx API
type TelnyxResponse struct {
	Data TelnyxMessageData `json:"data"`
}

type TelnyxMessageData struct {
	ID string `json:"id"`
	// Add other relevant fields as needed
}

// Inbound handles incoming Telnyx webhooks for MMS and SMS messages.
func (h *TelnyxHandler) Inbound(c iris.Context) error {
	// Parse the Telnyx webhook JSON payload
	var webhookPayload TelnyxWebhookPayload
	if err := c.ReadBody(&webhookPayload); err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.Inbound.Telnyx",
			"GenericError",
			logrus.ErrorLevel,
			nil,
			err,
		))
		c.StatusCode(http.StatusBadRequest)
		return nil
	}

	if len(webhookPayload.Data.Payload.To) <= 0 {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.Inbound.Telnyx",
			"CarrierNoDestinations",
			logrus.ErrorLevel,
			nil,
		))

		c.StatusCode(http.StatusBadRequest)
		return nil
	}

	if webhookPayload.Data.EventType != "message.received" {
		// ignore delivery?? or log it?? todo
		c.StatusCode(http.StatusOK)
		return nil
	}

	// Extract necessary fields
	from := webhookPayload.Data.Payload.From.PhoneNumber
	to := webhookPayload.Data.Payload.To[0].PhoneNumber
	body := webhookPayload.Data.Payload.Text
	messageID := webhookPayload.Data.Payload.ID
	// messagingProfileID := webhookPayload.Data.Payload.MessagingProfileID // if needed

	numMedia := len(webhookPayload.Data.Payload.Media)

	var files []MsgFile
	if numMedia > 0 {
		ff := h.fetchTelnyxMediaFiles(webhookPayload.Data.Payload.Media, messageID)
		if len(ff) <= 0 {
			c.StatusCode(http.StatusBadRequest)
			return nil
		}
		files = ff
	}

	// Handle MMS if media files are present
	if numMedia > 0 && len(files) > 0 {
		msg := MsgQueueItem{
			To:                to,
			From:              from,
			ReceivedTimestamp: time.Now(),
			Type:              MsgQueueItemType.MMS,
			Files:             files,
			SkipNumberCheck:   false,
			LogID:             messageID,
		}
		//h.gateway.MM4Server.msgToClientChannel <- mm4Message
		h.gateway.Router.CarrierMsgChan <- msg
	}

	// Handle SMS if body is present
	if strings.TrimSpace(body) != "" {
		smsMessages := splitSMS(body, 140)
		for _, smsBody := range smsMessages {
			sms := MsgQueueItem{
				To:                to,
				From:              from,
				ReceivedTimestamp: time.Now(),
				Type:              MsgQueueItemType.SMS,
				Message:           smsBody,
				LogID:             messageID,
			}
			h.gateway.Router.CarrierMsgChan <- sms
		}
	}

	// Respond with HTTP 200 OK
	c.StatusCode(http.StatusOK)
	return nil
}

// TelnyxWebhookPayload represents the structure of Telnyx inbound webhook
type TelnyxWebhookPayload struct {
	Data TelnyxEventData `json:"data"`
}

type TelnyxEventData struct {
	EventType  string               `json:"event_type"`
	ID         string               `json:"id"`
	OccurredAt string               `json:"occurred_at"`
	Payload    TelnyxMessagePayload `json:"payload"`
	RecordType string               `json:"record_type"`
}

type TelnyxMessagePayload struct {
	Direction string              `json:"direction"`
	ID        string              `json:"id"`
	From      TelnyxPhoneNumber   `json:"from"`
	To        []TelnyxPhoneNumber `json:"to"`
	Text      string              `json:"text"`
	Media     []TelnyxMedia       `json:"media"`
	// Add other relevant fields as needed
}

type TelnyxPhoneNumber struct {
	PhoneNumber string `json:"phone_number"`
	Carrier     string `json:"carrier"`
	LineType    string `json:"line_type"`
	Status      string `json:"status"`
}

type TelnyxMedia struct {
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

// fetchTelnyxMediaFiles retrieves media files from Telnyx webhook payload
func (h *TelnyxHandler) fetchTelnyxMediaFiles(media []TelnyxMedia, messageID string) []MsgFile {
	var files []MsgFile
	for _, m := range media {
		contentType := m.ContentType
		mediaURL := m.URL

		parts := strings.Split(contentType, "/")
		if len(parts) != 2 {
			// todo?
			continue
		}

		/*extension := parts[1]*/
		mediaSid := path.Base(mediaURL)
		filename := fmt.Sprintf("%s" /*.%s"*/, mediaSid /*, extension*/) // e.g., mediaSid.jpg

		// Fetch the media content
		contentBytes, err := fetchMediaContentTelnyx(mediaURL)
		if err != nil {
			var lm = h.gateway.LogManager
			lm.SendLog(lm.BuildLog(
				"Carrier.FetchMedia.Telnyx",
				"CarrierFetchMediaError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID": messageID,
				}, mediaURL,
			))
			continue
		}

		// Create the MsgFile struct
		file := MsgFile{
			Filename:    filename,
			ContentType: contentType,
			Content:     contentBytes,
		}
		files = append(files, file)
	}
	return files
}

// fetchMediaContentTelnyx retrieves media content from Telnyx URL
func fetchMediaContentTelnyx(mediaURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Non-OK HTTP status: %s", resp.Status)
	}

	contentBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading media content: %w", err)
	}

	return contentBytes, nil
}

// SendSMS sends an SMS message via Telnyx API
func (h *TelnyxHandler) SendSMS(sms *MsgQueueItem) error {
	// Construct the TelnyxMessage payload
	message := TelnyxMessage{
		From: sms.From,
		To:   sms.To,
		Text: sms.Message,
		// MessagingProfileID: os.Getenv("TELNYX_MESSAGING_PROFILE_ID"), // If needed
		// WebhookURL: "https://yourwebhook.url", // Optional
	}

	// Serialize to JSON
	payloadBytes, err := json.Marshal(message)
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to unmarshal (1) %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return err
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.telnyx.com/v2/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to build request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.password)

	// Perform the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to send request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return err
	}
	defer resp.Body.Close()

	// Read and parse response
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to read and parse: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to send SMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           sms.LogID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"response_body":   string(bodyBytes), // Include response body for debugging
				"msg":             sms,
			}, err,
		))
		return errors.New("failed to send SMS via Telnyx")
	}

	// Optionally, parse the response to get message ID
	var telnyxResp TelnyxResponse
	if err := json.Unmarshal(bodyBytes, &telnyxResp); err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to unmarshal (2): %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return err
	}

	/*logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", "telnyx", sms.From, sms.To)
	logf.AddField("telnyxMessageID", telnyxResp.Data.ID)
	logf.AddField("from", sms.From)
	logf.AddField("to", sms.To)
	logf.Print()*/

	return nil
}

// SendMMS sends an MMS message via Telnyx API
func (h *TelnyxHandler) SendMMS(mms *MsgQueueItem) error {
	// Construct the TelnyxMessage payload
	message := TelnyxMessage{
		From:      mms.From,
		To:        mms.To,
		Text:      "",        // Set this if you have text content
		Subject:   "Picture", // Or derive from your context
		MediaUrls: []string{},
	}

	var mediaUrls []string

	if len(mms.Files) > 0 {
		for _, i := range mms.Files {
			if strings.Contains(i.ContentType, "application/smil") {
				continue
			}

			id, err := h.gateway.saveMsgFileMedia(i)
			if err != nil {
				var lm = h.gateway.LogManager
				lm.SendLog(lm.BuildLog(
					"Carrier.SendMMS.Telnyx",
					"SaveMediaError",
					logrus.ErrorLevel,
					map[string]interface{}{
						"logID": mms.LogID,
					}, err,
				))
				return err
			}

			mediaUrls = append(mediaUrls, os.Getenv("SERVER_ADDRESS")+"/media/"+strconv.Itoa(int(id)))
		}
		message.MediaUrls = mediaUrls
	}

	// Optionally, set MessagingProfileID if sending alphanumeric messages
	messagingProfileID := os.Getenv("TELNYX_MESSAGING_PROFILE_ID")
	if messagingProfileID != "" {
		message.MessagingProfileID = messagingProfileID
	}

	// Serialize to JSON
	payloadBytes, err := json.Marshal(message)
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to marshal payload: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return err
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.telnyx.com/v2/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to build request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.password)

	// Perform the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to send request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return err
	}
	defer resp.Body.Close()

	// Read and parse response
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to read response body: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to send MMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           mms.LogID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"response_body":   string(bodyBytes), // Include response body for debugging
				"msg":             mms,
			}, nil,
		))
		return errors.New("failed to send MMS via Telnyx")
	}

	// Optionally, parse the response to get message ID
	var telnyxResp TelnyxResponse
	if err := json.Unmarshal(bodyBytes, &telnyxResp); err != nil {
		var lm = h.gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to unmarshal response: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return err
	}

	// Log success
	var lm = h.gateway.LogManager
	lm.SendLog(lm.BuildLog(
		"Carrier.SendMMS.Telnyx",
		"Successfully sent MMS",
		logrus.InfoLevel,
		map[string]interface{}{
			"logID":           mms.LogID,
			"telnyxMessageID": telnyxResp.Data.ID,
			"from":            mms.From,
			"to":              mms.To,
			"response_code":   resp.StatusCode,
			"response_status": resp.Status,
		}, nil,
	))

	return nil
}

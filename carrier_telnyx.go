package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	var lm = h.gateway.LogManager

	// Parse the Telnyx webhook JSON payload
	/*getBody, err := c.GetBody()
	if err != nil {
		return err
	}*/

	/*fmt.Println("body: " + string(getBody))*/

	var webhookPayload TelnyxWebhookPayload
	if err := c.ReadBody(&webhookPayload); err != nil {
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
		if webhookPayload.Data.EventType == "message.sent" {
			h.gateway.ConvoManager.HandleCarrierAck(webhookPayload.Data.Payload.ID, h.gateway.Router)
		}
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

	logID := primitive.NewObjectID().Hex()

	// Handle MMS if media files are present
	if numMedia > 0 && len(files) > 0 {
		msg := MsgQueueItem{
			To:                to,
			From:              from,
			ReceivedTimestamp: time.Now(),
			Type:              MsgQueueItemType.MMS,
			files:             files,
			SkipNumberCheck:   false,
			LogID:             logID,
			SourceCarrier:     h.carrier.Name,
		}
		//h.gateway.MM4Server.msgToClientChannel <- mm4Message
		h.gateway.Router.CarrierMsgChan <- msg
	}

	// Handle SMS if body is present
	if strings.TrimSpace(body) != "" {
		/*smsMessages := splitSMS(body, 140)*/
		sms := MsgQueueItem{
			To:                to,
			From:              from,
			ReceivedTimestamp: time.Now(),
			Type:              MsgQueueItemType.SMS,
			message:           body,
			LogID:             logID,
			SourceCarrier:     h.carrier.Name,
		}
		h.gateway.Router.CarrierMsgChan <- sms
		/*for _, smsBody := range smsMessages {

		}*/
	}

	lm.SendLog(lm.BuildLog(
		"Carrier.Telnyx.Inbound",
		"received",
		logrus.InfoLevel,
		map[string]interface{}{
			"logID":     logID,
			"carrierID": messageID,
			"from":      from,
			"to":        to,
		}, nil,
	))

	// Respond with HTTP 200 OK
	c.StatusCode(http.StatusOK)
	return nil
}

// TelnyxWebhookPayload represents the structure of Telnyx inbound webhook
type TelnyxWebhookPayload struct {
	Data struct {
		EventType  string    `json:"event_type"`
		ID         string    `json:"id"`
		OccurredAt time.Time `json:"occurred_at"`
		Payload    struct {
			Cc          []interface{} `json:"cc"`
			CompletedAt time.Time     `json:"completed_at"`
			Cost        struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"cost"`
			CostBreakdown struct {
				CarrierFee struct {
					Amount   string `json:"amount"`
					Currency string `json:"currency"`
				} `json:"carrier_fee"`
				Rate struct {
					Amount   string `json:"amount"`
					Currency string `json:"currency"`
				} `json:"rate"`
			} `json:"cost_breakdown"`
			Direction string        `json:"direction"`
			Encoding  string        `json:"encoding"`
			Errors    []interface{} `json:"errors"`
			From      struct {
				Carrier     string `json:"carrier"`
				LineType    string `json:"line_type"`
				PhoneNumber string `json:"phone_number"`
			} `json:"from"`
			ID                    string        `json:"id"`
			Media                 []TelnyxMedia `json:"media"`
			MessagingProfileId    string        `json:"messaging_profile_id"`
			OrganizationId        string        `json:"organization_id"`
			Parts                 int           `json:"parts"`
			ReceivedAt            time.Time     `json:"received_at"`
			RecordType            string        `json:"record_type"`
			SentAt                time.Time     `json:"sent_at"`
			Tags                  []interface{} `json:"tags"`
			TcrCampaignBillable   bool          `json:"tcr_campaign_billable"`
			TcrCampaignId         interface{}   `json:"tcr_campaign_id"`
			TcrCampaignRegistered interface{}   `json:"tcr_campaign_registered"`
			Text                  string        `json:"text"`
			To                    []struct {
				Carrier     string `json:"carrier"`
				LineType    string `json:"line_type"`
				PhoneNumber string `json:"phone_number"`
				Status      string `json:"status"`
			} `json:"to"`
			Type               string    `json:"type"`
			ValidUntil         time.Time `json:"valid_until"`
			WebhookFailoverUrl string    `json:"webhook_failover_url"`
			WebhookUrl         string    `json:"webhook_url"`
		} `json:"payload"`
		RecordType string `json:"record_type"`
	} `json:"data"`
	Meta struct {
		Attempt     int    `json:"attempt"`
		DeliveredTo string `json:"delivered_to"`
	} `json:"meta"`
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

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
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
func (h *TelnyxHandler) SendSMS(sms *MsgQueueItem) (string, error) {
	var lm = h.gateway.LogManager

	// Construct the TelnyxMessage payload
	message := TelnyxMessage{
		From: sms.From,
		To:   sms.To,
		Text: sms.message,
		// MessagingProfileID: os.Getenv("TELNYX_MESSAGING_PROFILE_ID"), // If needed
		// WebhookURL: "https://yourwebhook.url", // Optional
	}

	// Use carrier's ProfileID if configured (stored in database)
	if h.carrier.ProfileID != "" {
		message.MessagingProfileID = h.carrier.ProfileID
	}

	// Serialize to JSON
	payloadBytes, err := json.Marshal(message)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to unmarshal (1) %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.telnyx.com/v2/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to build request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.password)

	// Perform the request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to send request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}
	defer resp.Body.Close()

	// Read and parse response
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to read and parse: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {

		// check if it was blocked due to stop message
		if strings.Contains(string(bodyBytes), "Blocked due to STOP message") {
			lm.SendLog(lm.BuildLog(
				"Carrier.SendSMS.Telnyx",
				"Blocked due to STOP message",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID":           sms.LogID,
					"response_code":   resp.StatusCode,
					"response_status": resp.Status,
					"to":              sms.To,
					"from":            sms.From,
					"response_body":   string(bodyBytes), // Include response body for debugging
				}, err,
			))
			return "STOP_MESSAGE", errors.New("blocked due to STOP message")
		}

		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to send SMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           sms.LogID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"to":              sms.To,
				"from":            sms.From,
				"response_body":   string(bodyBytes), // Include response body for debugging
			}, err,
		))
		return "", errors.New("failed to send SMS via Telnyx")
	}

	// Optionally, parse the response to get message ID
	var telnyxResp TelnyxResponse
	if err := json.Unmarshal(bodyBytes, &telnyxResp); err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Telnyx",
			"Failed to unmarshal (2): %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
			}, err,
		))
		return "", err
	}

	return telnyxResp.Data.ID, nil
}

// SendMMS sends an MMS message via Telnyx API
func (h *TelnyxHandler) SendMMS(mms *MsgQueueItem) (string, error) {
	var lm = h.gateway.LogManager

	// Construct the TelnyxMessage payload
	message := TelnyxMessage{
		From:      mms.From,
		To:        mms.To,
		Text:      "",            // Set this if you have text content
		Subject:   "MMS Content", // Or derive from your context
		MediaUrls: []string{},
	}

	var mediaUrls []string

	if len(mms.files) > 0 {
		for _, i := range mms.files {
			if strings.Contains(i.ContentType, "application/smil") {
				continue
			}

			accessToken, err := h.gateway.saveMsgFileMedia(i)
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Carrier.SendMMS.Telnyx",
					"SaveMediaError",
					logrus.ErrorLevel,
					map[string]interface{}{
						"logID": mms.LogID,
					}, err,
				))
				return "", err
			}

			mediaUrls = append(mediaUrls, os.Getenv("SERVER_ADDRESS")+"/media/"+accessToken)
		}
		message.MediaUrls = mediaUrls
	}

	// Use carrier's ProfileID if configured (stored in database)
	if h.carrier.ProfileID != "" {
		message.MessagingProfileID = h.carrier.ProfileID
	}

	// Serialize to JSON
	payloadBytes, err := json.Marshal(message)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to marshal payload: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.telnyx.com/v2/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to build request: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.password)

	// Perform the request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
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
		return "", err
	}
	defer resp.Body.Close()

	// Read and parse response
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to read response body: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// check if it was blocked due to stop message
		if strings.Contains(string(bodyBytes), "Blocked due to STOP message") {
			lm.SendLog(lm.BuildLog(
				"Carrier.SendSMS.Telnyx",
				"Blocked due to STOP message",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID":           mms.LogID,
					"response_code":   resp.StatusCode,
					"response_status": resp.Status,
					"to":              mms.To,
					"from":            mms.From,
					"response_body":   string(bodyBytes), // Include response body for debugging
				}, err,
			))
			return "STOP_MESSAGE", errors.New("blocked due to STOP message")
		}

		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to send MMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":           mms.LogID,
				"response_code":   resp.StatusCode,
				"response_status": resp.Status,
				"to":              mms.To,
				"from":            mms.From,
				"response_body":   string(bodyBytes), // Include response body for debugging
			}, nil,
		))
		return "", errors.New("failed to send MMS via Telnyx")
	}

	// Optionally, parse the response to get message ID
	var telnyxResp TelnyxResponse
	if err := json.Unmarshal(bodyBytes, &telnyxResp); err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Telnyx",
			"Failed to unmarshal response: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
			}, err,
		))
		return "", err
	}

	return telnyxResp.Data.ID, nil
}

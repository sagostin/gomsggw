package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
)

// TelnyxHandler implements CarrierHandler for Telnyx
type TelnyxHandler struct {
	BaseCarrierHandler
	gateway *Gateway
	apiKey  string
}

// NewTelnyxHandler initializes a new TelnyxHandler
func NewTelnyxHandler(gateway *Gateway) *TelnyxHandler {
	return &TelnyxHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "telnyx"},
		gateway:            gateway,
		apiKey:             os.Getenv("TELNYX_API_KEY"),
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
func (h *TelnyxHandler) Inbound(c iris.Context, gateway *Gateway) error {
	// Initialize logging with a unique transaction ID
	transId := primitive.NewObjectID().Hex()
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Inbound,
	}
	logf.AddField("logID", transId)
	logf.AddField("carrier", "telnyx")

	// Parse the Telnyx webhook JSON payload
	var webhookPayload TelnyxWebhookPayload
	if err := c.ReadBody(&webhookPayload); err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Failed to parse webhook payload: %v", err)
		logf.Print()
		c.StatusCode(http.StatusBadRequest)
		return nil
	}

	if len(webhookPayload.Data.Payload.To) <= 0 {
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

	var files []File
	if numMedia > 0 {
		files, _ = fetchTelnyxMediaFiles(webhookPayload.Data.Payload.Media, messageID, logf)
	}

	// Handle MMS if media files are present
	if numMedia > 0 && len(files) > 0 {
		mm4Message := createMM4Message(from, to, body, messageID, files)
		logMMSReceived(logf, from, to, "", messageID, len(files))
		mm4Message.logID = transId
		h.gateway.MM4Server.inboundMessageCh <- mm4Message
	}

	// Handle SMS if body is present
	if strings.TrimSpace(body) != "" {
		smsMessages := splitSMS(body, 140)
		for _, smsBody := range smsMessages {
			sms := createSMSMessage(from, to, smsBody, messageID, "", transId)
			logSMSReceived(logf, from, to, "", messageID, len(sms.Content))
			h.gateway.SMPPServer.smsInboundChannel <- sms
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
func fetchTelnyxMediaFiles(media []TelnyxMedia, messageID string, logf LoggingFormat) ([]File, error) {
	var files []File
	for i, m := range media {
		contentType := m.ContentType
		mediaURL := m.URL

		parts := strings.Split(contentType, "/")
		if len(parts) != 2 {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Invalid MediaContentType%d: %s", i, contentType)
			logf.Print()
			continue
		}

		/*extension := parts[1]*/
		mediaSid := path.Base(mediaURL)
		filename := fmt.Sprintf("%s" /*.%s"*/, mediaSid /*, extension*/) // e.g., mediaSid.jpg

		// Fetch the media content
		contentBytes, err := fetchMediaContentTelnyx(mediaURL)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Error fetching MediaUrl%d: %v", i, err)
			logf.AddField("messageID", messageID)
			logf.Print()
			continue
		}

		// Create the File struct
		file := File{
			Filename:    filename,
			ContentType: contentType,
			Content:     contentBytes,
		}
		files = append(files, file)
	}
	return files, nil
}

// fetchMediaContentTelnyx retrieves media content from Telnyx URL
func fetchMediaContentTelnyx(mediaURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, err
	}

	// Telnyx may require Bearer token for media retrieval
	/*apiKey := os.Getenv("TELNYX_API_KEY")
	if apiKey == "" {
		return nil, errors.New("TELNYX_API_KEY not set")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)*/

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
		return nil, fmt.Errorf("Error reading media content: %w", err)
	}

	return contentBytes, nil
}

// SendSMS sends an SMS message via Telnyx API
func (h *TelnyxHandler) SendSMS(sms *SMPPMessage) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Outbound,
	}
	logf.AddField("carrier", "telnyx")
	logf.AddField("type", "sms")
	logf.AddField("logID", sms.logID)

	// Construct the TelnyxMessage payload
	message := TelnyxMessage{
		From: sms.From,
		To:   sms.To,
		Text: sms.Content,
		// MessagingProfileID: os.Getenv("TELNYX_MESSAGING_PROFILE_ID"), // If needed
		// WebhookURL: "https://yourwebhook.url", // Optional
	}

	// Serialize to JSON
	payloadBytes, err := json.Marshal(message)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to marshal SMS payload"
		logf.Print()
		return err
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.telnyx.com/v2/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to create HTTP request for SendSMS"
		logf.Print()
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.apiKey)

	// Perform the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "HTTP request failed for SendSMS"
		logf.Print()
		return err
	}
	defer resp.Body.Close()

	// Read and parse response
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to read response body for SendSMS"
		logf.Print()
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Non-OK HTTP status for SendSMS: %s, Response: %s", resp.Status, string(bodyBytes))
		logf.Print()
		return errors.New("failed to send SMS via Telnyx")
	}

	// Optionally, parse the response to get message ID
	var telnyxResp TelnyxResponse
	if err := json.Unmarshal(bodyBytes, &telnyxResp); err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to parse Telnyx SendSMS response"
		logf.Print()
		return err
	}

	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", "telnyx", sms.From, sms.To)
	logf.AddField("telnyxMessageID", telnyxResp.Data.ID)
	logf.AddField("from", sms.From)
	logf.AddField("to", sms.To)
	logf.Print()

	return nil
}

// SendMMS sends an MMS message via Telnyx API
func (h *TelnyxHandler) SendMMS(mms *MM4Message) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Outbound,
	}

	logf.AddField("carrier", "telnyx")
	logf.AddField("type", "mms")
	logf.AddField("logID", mms.logID)

	// Construct the TelnyxMessage payload
	message := TelnyxMessage{
		From: mms.From,
		To:   mms.To,
		Text:/*string(mms.Content)*/ "",
		Subject:   "Picture", // Or derive from your context
		MediaUrls: []string{},
	}

	// Add media URLs
	if len(mms.Files) > 0 {
		for _, file := range mms.Files {
			if strings.Contains(file.ContentType, "application/smil") {
				continue
			}

			// Assume you have a function to upload media and get a publicly accessible URL
			mediaURL, err := h.uploadMediaAndGetURL(file)
			if err != nil {
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf("Failed to upload media: %v", err)
				logf.Print()
				continue
			}
			message.MediaUrls = append(message.MediaUrls, mediaURL)
		}
	}

	// Optionally, set MessagingProfileID if sending alphanumeric messages
	messagingProfileID := os.Getenv("TELNYX_MESSAGING_PROFILE_ID")
	if messagingProfileID != "" {
		message.MessagingProfileID = messagingProfileID
	}

	// Serialize to JSON
	payloadBytes, err := json.Marshal(message)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to marshal MMS payload"
		logf.Print()
		return err
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.telnyx.com/v2/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to create HTTP request for SendMMS"
		logf.Print()
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.apiKey)

	// Perform the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "HTTP request failed for SendMMS"
		logf.Print()
		return err
	}
	defer resp.Body.Close()

	// Read and parse response
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to read response body for SendMMS"
		logf.Print()
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Non-OK HTTP status for SendMMS: %s, Response: %s", resp.Status, string(bodyBytes))
		logf.Print()
		return errors.New("failed to send MMS via Telnyx")
	}

	// Optionally, parse the response to get message ID
	var telnyxResp TelnyxResponse
	if err := json.Unmarshal(bodyBytes, &telnyxResp); err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to parse Telnyx SendMMS response"
		logf.Print()
		return err
	}

	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", "telnyx", mms.From, mms.To)
	logf.AddField("telnyxMessageID", telnyxResp.Data.ID)
	logf.AddField("from", mms.From)
	logf.AddField("to", mms.To)
	logf.Print()

	return nil
}

// uploadMediaAndGetURL uploads media to your server and returns a publicly accessible URL
func (h *TelnyxHandler) uploadMediaAndGetURL(file File) (string, error) {
	// Implement the logic to save the file to your storage (e.g., MongoDB, AWS S3)
	// and return the accessible URL.

	// Example using a hypothetical function saveBase64ToMongoDB
	id, err := saveBase64ToMongoDB(h.gateway.MongoClient, file.Filename, string(file.Content), file.ContentType)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/media/%s", os.Getenv("SERVER_ADDRESS"), id), nil
}

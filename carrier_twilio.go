package main

import (
	"fmt"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// TwilioHandler implements CarrierHandler for Twilio
type TwilioHandler struct {
	BaseCarrierHandler
	client  *twilio.RestClient
	gateway *Gateway
}

func NewTwilioHandler(gateway *Gateway) *TwilioHandler {
	return &TwilioHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "twilio"},
		client: twilio.NewRestClientWithParams(twilio.ClientParams{
			Username: os.Getenv("TWILIO_ACCOUNT_SID"),
			Password: os.Getenv("TWILIO_AUTH_TOKEN"),
		}),
		gateway: gateway,
	}
}

// Inbound handles incoming Twilio webhooks for MMS and SMS messages.
func (h *TwilioHandler) Inbound(c iris.Context, gateway *Gateway) error {
	// Initialize logging with a unique transaction ID
	transId := primitive.NewObjectID().Hex()
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Inbound,
	}
	logf.AddField("logID", transId)
	logf.AddField("carrier", "twilio")

	// Parse the number of media items
	numMediaStr := c.FormValue("NumMedia")
	numMedia, err := strconv.Atoi(numMediaStr)
	if err != nil {
		numMedia = 0
	}

	// Common parameters
	from := c.FormValue("From")
	to := c.FormValue("To")
	body := c.FormValue("Body")
	messageSid := c.FormValue("MessageSid")
	accountSid := c.FormValue("AccountSid")

	logf.AddField("messageSid", messageSid)
	logf.AddField("accountSid", accountSid)

	var files []MsgFile

	// Fetch media files if present
	if numMedia > 0 {
		files, err = fetchMediaFiles(c, numMedia, accountSid, messageSid, logf)
		if err != nil {
			// Error already logged in fetchMediaFiles
			// Proceed to handle SMS if body is present
		}
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
			LogID:             transId,
		}
		logMMSReceived(logf, from, to, "", messageSid, len(files))
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
				LogID:             transId,
			}
			logSMSReceived(logf, from, to, "", messageSid, len(sms.Message))
			h.gateway.Router.CarrierMsgChan <- sms
		}
	}

	// Handle SMS if body is present
	if strings.TrimSpace(body) != "" {
		smsMessages := splitSMS(body, 140)
		for _, smsBody := range smsMessages {
			sms := MsgQueueItem{
				To:                to,
				From:              from,
				ReceivedTimestamp: time.Now(),
				QueuedTimestamp:   time.Time{},
				Type:              MsgQueueItemType.SMS,
				Files:             nil,
				Message:           smsBody,
				SkipNumberCheck:   false,
				LogID:             transId,
			}
			//sms := createSMSMessage(from, to, smsBody, messageSid, accountSid, transId)
			logSMSReceived(logf, from, to, accountSid, messageSid, len(sms.Message))
			// gateway.SMPPServer.msgToClientChannel <- sms
		}
	}

	// Prepare the TwiML response
	twiml := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Response>\n</Response>"

	// Set the correct Msg-Type header
	c.Header("Msg-Type", "application/xml")

	_, err = c.Write([]byte(twiml))
	if err != nil {
		return err
	}

	// Send the TwiML response
	return err
}

// fetchMediaFiles retrieves media files from Twilio and returns a slice of MsgFile structs.
func fetchMediaFiles(c iris.Context, numMedia int, accountSid, messageSid string, logf LoggingFormat) ([]MsgFile, error) {
	var files []MsgFile

	for i := 0; i < numMedia; i++ {
		mediaURL := c.FormValue(fmt.Sprintf("MediaUrl%d", i))
		contentType := c.FormValue(fmt.Sprintf("MediaContentType%d", i))
		mediaSid := path.Base(mediaURL)
		parts := strings.Split(contentType, "/")

		if len(parts) != 2 {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Invalid MediaContentType%d: %s", i, contentType)
			logf.Print()
			continue
		}

		extension := parts[1]
		filename := fmt.Sprintf("%s.%s", mediaSid, extension) // e.g., mediaSid.jpg

		// Fetch the media content
		req, err := http.NewRequest("GET", mediaURL, nil)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Error creating request for MediaUrl%d: %v", i, err)
			logf.AddField("accountSid", accountSid)
			logf.AddField("messageSid", messageSid)
			logf.Print()
			continue
		}

		// Set Basic Auth
		twilioAccountSid := os.Getenv("TWILIO_ACCOUNT_SID")
		twilioAuthToken := os.Getenv("TWILIO_AUTH_TOKEN")
		req.SetBasicAuth(twilioAccountSid, twilioAuthToken)

		// Perform the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Error fetching MediaUrl%d: %v", i, err)
			logf.AddField("accountSid", accountSid)
			logf.AddField("messageSid", messageSid)
			logf.Print()
			continue
		}
		defer resp.Body.Close()

		// Check for successful response
		if resp.StatusCode != http.StatusOK {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Non-OK HTTP status for MediaUrl%d: %s", i, resp.Status)
			logf.AddField("accountSid", accountSid)
			logf.AddField("messageSid", messageSid)
			logf.Print()
			continue
		}

		// Read the content into memory
		contentBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Error reading response body for MediaUrl%d: %v", i, err)
			logf.AddField("accountSid", accountSid)
			logf.AddField("messageSid", messageSid)
			logf.Print()
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

	return files, nil
}

// splitSMS splits the SMS content into multiple messages, each not exceeding maxBytes.
// It ensures that multi-byte characters are not split.
func splitSMS(body string, maxBytes int) []string {
	var smsSegments []string
	currentSegment := ""
	currentBytes := 0

	for _, r := range body {
		runeBytes := len(string(r))
		if currentBytes+runeBytes > maxBytes {
			if currentSegment != "" {
				smsSegments = append(smsSegments, currentSegment)
			}
			currentSegment = string(r)
			currentBytes = runeBytes
		} else {
			currentSegment += string(r)
			currentBytes += runeBytes
		}
	}

	if currentSegment != "" {
		smsSegments = append(smsSegments, currentSegment)
	}

	return smsSegments
}

// logMMSReceived logs details about the received MMS.
func logMMSReceived(logf LoggingFormat, from, to string, accountSid, messageSid string, numFiles int) {
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)
	logf.Level = logrus.InfoLevel
	logf.AddField("accountSid", accountSid)
	logf.AddField("messageSid", messageSid)
	logf.AddField("from", from)
	logf.AddField("to", to)
	logf.AddField("type", "mms")
	logf.AddField("numFiles", numFiles)
	logf.Print()
}

// logSMSReceived logs details about the received SMS.
func logSMSReceived(logf LoggingFormat, from, to string, accountSid, messageSid string, contentLength int) {
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)
	logf.Level = logrus.InfoLevel
	logf.AddField("accountSid", accountSid)
	logf.AddField("messageSid", messageSid)
	logf.AddField("from", from)
	logf.AddField("to", to)
	logf.AddField("type", "sms")
	logf.AddField("contentLength", contentLength)
	logf.Print()
}

func (h *TwilioHandler) SendSMS(sms *MsgQueueItem) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Outbound,
	}
	logf.AddField("carrier", "twilio")
	logf.AddField("type", "sms")
	logf.AddField("logID", sms.LogID)

	params := &twilioApi.CreateMessageParams{}
	params.SetTo(sms.To)
	params.SetFrom(sms.From)
	params.SetBody(sms.Message)

	// todo support for MMS??

	msg, err := h.client.Api.CreateMessage(params)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		// direction, carrier, type (mms/sms), sid/msid, from, to
		logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", logf.AdditionalData["carrier"], sms.From, sms.To)
		if msg != nil {
			logf.AddField("messageSid", *msg.Sid)
		}
		return logf.ToError()
	}

	logf.Level = logrus.InfoLevel
	// direction, carrier, type (mms/sms), sid/msid, from, to
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", logf.AdditionalData["carrier"], sms.From, sms.To)
	logf.AddField("messageSid", *msg.Sid)
	logf.AddField("from", sms.From)
	logf.AddField("to", sms.To)
	logf.Print()

	return nil
}

func (h *TwilioHandler) SendMMS(mms *MsgQueueItem) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Outbound,
	}

	logf.AddField("carrier", "twilio")
	logf.AddField("type", "mms")
	logf.AddField("logID", mms.LogID)

	params := &twilioApi.CreateMessageParams{}
	// clean to & from

	params.SetTo(mms.To)
	params.SetFrom(mms.From)
	params.SetBody("")

	var mediaUrls []string

	if len(mms.Files) > 0 {
		for _, i := range mms.Files {
			if strings.Contains(i.ContentType, "application/smil") {
				continue
			}

			id, err := saveBase64ToMongoDB(h.gateway.MongoClient, i)
			if err != nil {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				return logf.ToError()
			}

			mediaUrls = append(mediaUrls, os.Getenv("SERVER_ADDRESS")+"/media/"+id)
		}
		params.MediaUrl = &mediaUrls
	}

	// todo support for MMS??

	msg, err := h.client.Api.CreateMessage(params)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", logf.AdditionalData["carrier"], mms.From, mms.To)

		if msg != nil {
			logf.AddField("messageSid", *msg.Sid)
		}
		return logf.ToError()
	}

	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", logf.AdditionalData["carrier"], mms.From, mms.To)
	logf.AddField("messageSid", *msg.Sid)
	logf.AddField("from", mms.From)
	logf.AddField("to", mms.To)
	logf.Print()

	return nil
}

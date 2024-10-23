package main

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io"
	"net/http"
	"net/textproto"
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
func (h *TwilioHandler) Inbound(c *fiber.Ctx, gateway *Gateway) error {
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

	var files []File

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
		mm4Message := createMM4Message(from, to, body, messageSid, files)
		logMMSReceived(logf, from, to, accountSid, messageSid, len(files))
		mm4Message.logID = transId
		gateway.MM4Server.inboundMessageCh <- mm4Message
	}

	// Handle SMS if body is present
	if strings.TrimSpace(body) != "" {
		smsMessages := splitSMS(body, 140)
		for _, smsBody := range smsMessages {
			sms := createSMSMessage(from, to, smsBody, messageSid, accountSid, transId)
			logSMSReceived(logf, from, to, accountSid, messageSid, len(sms.Content))
			gateway.SMPPServer.smsInboundChannel <- sms
		}
	}

	// Prepare the TwiML response
	twiml := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Response>\n</Response>"

	// Set the correct Content-Type header
	c.Set("Content-Type", "application/xml")

	// Send the TwiML response
	return c.Send([]byte(twiml))
}

// fetchMediaFiles retrieves media files from Twilio and returns a slice of File structs.
func fetchMediaFiles(c *fiber.Ctx, numMedia int, accountSid, messageSid string, logf LoggingFormat) ([]File, error) {
	var files []File

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

// createMM4Message constructs an MM4Message with the provided media files.
func createMM4Message(from, to, body, messageSid string, files []File) *MM4Message {
	headers := textproto.MIMEHeader{}
	headers.Set("From", fmt.Sprintf("%s/TYPE=PLMN", from))
	headers.Set("To", fmt.Sprintf("%s/TYPE=PLMN", to))
	headers.Set("MIME-Version", "1.0")
	headers.Set("X-Mms-3GPP-Mms-Version", "6.10.0")
	headers.Set("X-Mms-Message-Type", "MM4_forward.REQ")
	headers.Set("X-Mms-Message-Id", fmt.Sprintf("<%s@yourdomain.com>", messageSid)) // Replace 'yourdomain.com' appropriately
	headers.Set("X-Mms-Transaction-Id", messageSid)
	headers.Set("X-Mms-Ack-Request", "Yes")

	originatorSystem := os.Getenv("MM4_ORIGINATOR_SYSTEM") // e.g., "system@108.165.150.61"
	if originatorSystem == "" {
		originatorSystem = "system@yourdomain.com" // Fallback or default value
	}
	headers.Set("X-Mms-Originator-System", originatorSystem)
	headers.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	// Content-Type will be set in sendMM4Message based on whether SMIL is included or not

	return &MM4Message{
		From:          from,
		To:            to,
		Content:       []byte(body),
		Headers:       headers,
		TransactionID: messageSid,
		MessageID:     messageSid,
		Files:         files,
	}
}

// createSMSMessage constructs an SMPPMessage (SMS) with the provided text.
func createSMSMessage(from, to, body, messageSid, accountSid, logID string) SMPPMessage {
	return SMPPMessage{
		From:    from,
		To:      to,
		Content: body,
		CarrierData: map[string]string{
			"MessageSid": messageSid,
			"AccountSid": accountSid,
		},
		logID: logID,
	}
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

// Inbound handles incoming Twilio webhooks for MMS and SMS messages.
/*func (h *TwilioHandler) Inbound(c *fiber.Ctx, gateway *Gateway) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Inbound,
	}
	transId := primitive.NewObjectID().Hex()
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

	var files []File
	if numMedia > 0 {
		// Handle MMS
		for i := 0; i < numMedia; i++ {
			mediaUrl := c.FormValue(fmt.Sprintf("MediaUrl%d", i))
			contentType := c.FormValue(fmt.Sprintf("MediaContentType%d", i))
			mediaSid := path.Base(mediaUrl)
			parts := strings.Split(contentType, "/")
			if len(parts) != 2 {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)
				logf.Print()
				continue
			}
			extension := parts[1]
			filename := fmt.Sprintf("%s.%s", mediaSid, extension) // e.g., mediaSid.jpg

			// Fetch the media content
			req, err := http.NewRequest("GET", mediaUrl, nil)
			if err != nil {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)

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
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)

				logf.AddField("accountSid", accountSid)
				logf.AddField("messageSid", messageSid)
				logf.Print()
				continue
			}
			defer resp.Body.Close()

			// Check for successful response
			if resp.StatusCode != http.StatusOK {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)

				logf.AddField("accountSid", accountSid)
				logf.AddField("messageSid", messageSid)
				logf.Print()
				continue
			}

			// Read the content into memory
			contentBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)

				logf.AddField("accountSid", accountSid)
				logf.AddField("messageSid", messageSid)
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
	}

	if numMedia > 0 {
		// Handle MMS
		headers := textproto.MIMEHeader{}
		headers.Set("From", fmt.Sprintf("%s/TYPE=PLMN", from))
		headers.Set("To", fmt.Sprintf("%s/TYPE=PLMN", to))
		headers.Set("MIME-Version", "1.0")
		headers.Set("X-Mms-3GPP-Mms-Version", "6.10.0")
		headers.Set("X-Mms-Message-Type", "MM4_forward.REQ")
		headers.Set("X-Mms-Message-Id", fmt.Sprintf("<%s@%s>", "yourdomain.com")) // todo Replace 'yourdomain.com' appropriately
		headers.Set("X-Mms-Transaction-Id", messageSid)
		headers.Set("X-Mms-Ack-Request", "Yes")
		originatorSystem := os.Getenv("MM4_ORIGINATOR_SYSTEM") // e.g., "system@108.165.150.61"
		if originatorSystem == "" {
			originatorSystem = "system@yourdomain.com" // Fallback or default value
		}
		headers.Set("X-Mms-Originator-System", originatorSystem)
		headers.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
		// Content-Type will be set in sendMM4Message based on whether SMIL is included or not

		// Create an MM4Message
		mm4Message := &MM4Message{
			From:      from,
			To:        to,
			Content:   []byte(body),
			Headers:   headers,
			logID:     transId,
			MessageID: messageSid,
			Files:     files,
		}
		logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)
		logf.Level = logrus.InfoLevel
		logf.AddField("accountSid", accountSid)
		logf.AddField("messageSid", messageSid)
		logf.AddField("from", from)
		logf.AddField("to", to)
		logf.AddField("type", "mms")
		logf.Print()

		// Send the MM4Message over to the MM4 server's inbound channel
		h.gateway.MM4Server.inboundMessageCh <- mm4Message
	} else {
		// Handle SMS
		sms := SMPPMessage{
			From:    from,
			To:      to,
			Content: body,
			CarrierData: map[string]string{
				"MessageSid": messageSid,
				"AccountSid": accountSid,
			},
			logID: transId,
		}
		logf.Message = fmt.Sprintf(LogMessages.Transaction, "inbound", logf.AdditionalData["carrier"], from, to)
		logf.Level = logrus.InfoLevel
		logf.AddField("accountSid", accountSid)
		logf.AddField("messageSid", messageSid)
		logf.AddField("from", from)
		logf.AddField("to", to)
		logf.AddField("type", "sms")
		logf.Print()

		// Send the SMS message over to the SMPP server's outbound channel
		h.gateway.SMPPServer.smsInboundChannel <- sms
	}

	// Prepare the TwiML response
	twiml := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Response>\n</Response>"

	// Set the correct Content-Type header
	c.Set("Content-Type", "application/xml")

	// Send the TwiML response
	return c.Send([]byte(twiml))
}*/

func (h *TwilioHandler) SendSMS(sms *SMPPMessage) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Outbound,
	}
	logf.AddField("carrier", "twilio")
	logf.AddField("type", "sms")
	logf.AddField("logID", sms.logID)

	params := &twilioApi.CreateMessageParams{}
	params.SetTo(sms.To)
	params.SetFrom(sms.From)
	params.SetBody(sms.Content)

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

func (h *TwilioHandler) SendMMS(mms *MM4Message) error {
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Outbound,
	}

	logf.AddField("carrier", "twilio")
	logf.AddField("type", "mms")
	logf.AddField("logID", mms.logID)

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

			id, err := saveBase64ToMongoDB(h.gateway.MongoClient, i.Filename, string(i.Content), i.ContentType)
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

package main

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
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
	logf := LoggingFormat{
		Path:     "carrier_twilio",
		Function: "Inbound",
	}

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
				logf.Message = "Error receiving MMS from Twilio - From: " + from + " To: " + to
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
				logf.TransactionID = "twilio_" + messageSid
				logf.Message = "Error creating media request for MMS from Twilio - From: " + from + " To: " + to
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
				logf.TransactionID = "twilio_" + messageSid
				logf.Message = "Error fetching media content for MMS from Twilio - From: " + from + " To: " + to
				logf.Print()
				continue
			}
			defer resp.Body.Close()

			// Check for successful response
			if resp.StatusCode != http.StatusOK {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.TransactionID = "twilio_" + messageSid
				logf.Message = "Failed to fetch media content for MMS from Twilio - From: " + from + " To: " + to + " - Status: " + strconv.Itoa(resp.StatusCode)
				logf.Print()
				continue
			}

			// Read the content into memory
			contentBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.TransactionID = "twilio_" + messageSid
				logf.Message = "Error reading content bytes for MMS from Twilio - From: " + from + " To: " + to
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
		headers.Set("X-Mms-Message-Id", fmt.Sprintf("<%s@%s>", messageSid, "yourdomain.com")) // todo Replace 'yourdomain.com' appropriately
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
			From:          from,
			To:            to,
			Content:       []byte(body),
			Headers:       headers,
			TransactionID: messageSid,
			MessageID:     messageSid,
			Files:         files,
		}

		logf.Print()

		// Send the MM4Message over to the MM4 server's inbound channel
		h.gateway.MM4Server.inboundMessageCh <- mm4Message
	} else {
		// Handle SMS
		sms := CarrierMessage{
			From:    from,
			To:      to,
			Content: body,
			CarrierData: map[string]string{
				"MessageSid": messageSid,
				"AccountSid": accountSid,
			},
		}
		// Send the SMS message over to the SMPP server's outbound channel
		h.gateway.SMPPServer.smsInboundChannel <- sms
	}

	// Prepare the TwiML response
	twiml := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Response>\n</Response>"

	// Set the correct Content-Type header
	c.Set("Content-Type", "application/xml")

	// Send the TwiML response
	return c.Send([]byte(twiml))
}

func (h *TwilioHandler) SendSMS(sms *CarrierMessage) error {
	logf := LoggingFormat{
		Path:     "carrier_twilio",
		Function: "SendSMS",
	}
	params := &twilioApi.CreateMessageParams{}
	params.SetTo(sms.To)
	params.SetFrom(sms.From)
	params.SetBody(sms.Content)

	// todo support for MMS??

	_, err := h.client.Api.CreateMessage(params)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "Error sending SMS to Twilio - From: " + sms.From + " To: " + sms.To
		return logf.ToError()
	}

	logf.Level = logrus.InfoLevel
	logf.Message = "Sent SMS via Twilio - From: " + sms.From + " To: " + sms.To
	logf.Print()

	return nil
}

func (h *TwilioHandler) SendMMS(sms *MM4Message) error {
	logf := LoggingFormat{
		Path:     "carrier_twilio",
		Function: "SendMMS",
	}

	params := &twilioApi.CreateMessageParams{}
	// clean to & from
	toClean := strings.Split(sms.To, "/")
	fromClean := strings.Split(sms.From, "/")

	params.SetTo(toClean[0])
	params.SetFrom(fromClean[0])
	params.SetBody("")

	var mediaUrls []string

	if len(sms.Files) > 0 {
		for _, i := range sms.Files {
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
		logf.TransactionID = "twilio_" + *msg.Sid
		logf.Message = "Failed to send MMS to Twilio - From: " + fromClean[0] + " To: " + toClean[0]
		return logf.ToError()
	}

	logf.Level = logrus.InfoLevel
	logf.Message = "Sent MMS via Twilio - From: " + fromClean[0] + " To: " + toClean[0]
	logf.Print()

	return nil
}

package main

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"io"
	"log"
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

func NewTwilioHandler(logger *CustomLogger, gateway *Gateway) *TwilioHandler {
	return &TwilioHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "twilio", logger: logger},
		client: twilio.NewRestClientWithParams(twilio.ClientParams{
			Username: os.Getenv("TWILIO_ACCOUNT_SID"),
			Password: os.Getenv("TWILIO_AUTH_TOKEN"),
		}),
		gateway: gateway,
	}
}

// Inbound handles incoming Twilio webhooks for MMS and SMS messages.
func (h *TwilioHandler) Inbound(c *fiber.Ctx, gateway *Gateway) error {
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
				log.Printf("Invalid MediaContentType%d: %s", i, contentType)
				continue
			}
			extension := parts[1]
			filename := fmt.Sprintf("%s.%s", mediaSid, extension) // e.g., mediaSid.jpg

			// Fetch the media content
			req, err := http.NewRequest("GET", mediaUrl, nil)
			if err != nil {
				log.Printf("Error creating request for media URL: %v", err)
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
				log.Printf("Error fetching media content: %v", err)
				continue
			}
			defer resp.Body.Close()

			// Check for successful response
			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to fetch media content: Status %d", resp.StatusCode)
				continue
			}

			// Read the content into memory
			contentBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("Error reading media content: %v", err)
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
		headers.Set("X-Mms-Message-Id", fmt.Sprintf("<%s@%s>", messageSid, "yourdomain.com")) // Replace 'yourdomain.com' appropriately
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
	params := &twilioApi.CreateMessageParams{}
	params.SetTo(sms.To)
	params.SetFrom(sms.From)
	params.SetBody(sms.Content)

	// todo support for MMS??

	_, err := h.client.Api.CreateMessage(params)
	if err != nil {
		return fmt.Errorf("error sending GatewayMessage via Twilio: %v", err)
	}

	h.logger.Log(fmt.Sprintf("GatewayMessage sent successfully via Twilio: From %s To %s", sms.From, sms.To))
	return nil
}

func (h *TwilioHandler) SendMMS(sms *MM4Message) error {
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
				println("skipping smil, piece of shit")
				continue
			}

			id, err := saveBase64ToMongoDB(h.gateway.MongoClient, i.Filename, string(i.Content), i.ContentType)
			if err != nil {
				return err
			}

			mediaUrls = append(mediaUrls, os.Getenv("SERVER_ADDRESS")+"/media/"+id)
		}
		params.MediaUrl = &mediaUrls
	}

	// todo support for MMS??

	_, err := h.client.Api.CreateMessage(params)
	if err != nil {
		return fmt.Errorf("error sending GatewayMessage via Twilio: %v", err)
	}

	h.logger.Log(fmt.Sprintf("GatewayMessage sent successfully via Twilio: From %s To %s", sms.From, sms.To))
	return nil
}

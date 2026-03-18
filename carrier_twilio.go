package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TwilioHandler implements CarrierHandler for Twilio
type TwilioHandler struct {
	BaseCarrierHandler
	client   *twilio.RestClient
	gateway  *Gateway
	carrier  *Carrier
	username string // Account SID (used for media fetching auth)
	password string // Auth Token (used for media fetching auth)
}

func NewTwilioHandler(gateway *Gateway, carrier *Carrier, decryptedUsername string, decryptedPassword string) *TwilioHandler {
	return &TwilioHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "twilio"},
		client: twilio.NewRestClientWithParams(twilio.ClientParams{
			Username: decryptedUsername,
			Password: decryptedPassword,
		}),
		gateway:  gateway,
		carrier:  carrier,
		username: decryptedUsername,
		password: decryptedPassword,
	}
}

// Inbound handles incoming Twilio webhooks for MMS and SMS messages.
func (h *TwilioHandler) Inbound(c iris.Context) error {
	lm := h.gateway.LogManager

	// Initialize logging with a unique transaction ID
	transId := primitive.NewObjectID().Hex()

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

	var files []MsgFile

	// Fetch media files if present
	if numMedia > 0 {
		ff := h.fetchMediaFiles(c, numMedia, messageSid)
		if len(ff) <= 0 {
			c.StatusCode(http.StatusBadRequest)
			return nil
		}
		files = ff
	}

	// Handle MMS if media files are present
	if numMedia > 0 && len(files) > 0 {
		// Calculate original file sizes
		var originalSizeBytes int
		for _, f := range files {
			originalSizeBytes += len(f.Content)
		}

		msg := MsgQueueItem{
			To:                to,
			From:              from,
			ReceivedTimestamp: time.Now(),
			Type:              MsgQueueItemType.MMS,
			files:             files,
			SkipNumberCheck:   false,
			LogID:             transId,
			SourceCarrier:     h.carrier.Name,
			OriginalSizeBytes: originalSizeBytes,
		}
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
				message:           smsBody,
				LogID:             transId,
				SourceCarrier:     h.carrier.Name,
			}
			h.gateway.Router.CarrierMsgChan <- sms
		}
	}

	lm.SendLog(lm.BuildLog(
		"Carrier.Twilio.Inbound",
		"received",
		logrus.InfoLevel,
		map[string]interface{}{
			"logID":     transId,
			"carrierID": messageSid,
			"from":      from,
			"to":        to,
		}, nil,
	))

	// Prepare the TwiML response
	twiml := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Response>\n</Response>"

	// Set the correct Content-Type header
	c.Header("Content-Type", "application/xml")

	_, err = c.Write([]byte(twiml))
	if err != nil {
		return err
	}

	return nil
}

// fetchMediaFiles retrieves media files from Twilio and returns a slice of MsgFile structs.
// Uses the carrier's stored credentials (Account SID + Auth Token) for Basic Auth.
func (h *TwilioHandler) fetchMediaFiles(c iris.Context, numMedia int, messageSid string) []MsgFile {
	lm := h.gateway.LogManager
	var files []MsgFile

	for i := 0; i < numMedia; i++ {
		mediaURL := c.FormValue(fmt.Sprintf("MediaUrl%d", i))
		contentType := c.FormValue(fmt.Sprintf("MediaContentType%d", i))
		mediaSid := path.Base(mediaURL)
		parts := strings.Split(contentType, "/")

		if len(parts) != 2 {
			continue
		}

		extension := parts[1]
		filename := fmt.Sprintf("%s.%s", mediaSid, extension)

		// Fetch the media content
		req, err := http.NewRequest("GET", mediaURL, nil)
		if err != nil {
			continue
		}

		// Use the carrier's stored credentials for auth
		req.SetBasicAuth(h.username, h.password)

		// Perform the request
		client := &http.Client{
			Timeout: 30 * time.Second,
		}
		resp, err := client.Do(req)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Carrier.FetchMedia.Twilio",
				"CarrierFetchMediaError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID": messageSid,
				}, mediaURL,
			))
			continue
		}
		defer resp.Body.Close()

		// Check for successful response
		if resp.StatusCode != http.StatusOK {
			lm.SendLog(lm.BuildLog(
				"Carrier.FetchMedia.Twilio",
				"CarrierFetchMediaNonOK",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID":      messageSid,
					"statusCode": resp.StatusCode,
					"mediaURL":   mediaURL,
				},
			))
			continue
		}

		// Read the content into memory
		contentBytes, err := io.ReadAll(resp.Body)
		if err != nil {
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

// SendSMS sends an SMS message via the Twilio API.
func (h *TwilioHandler) SendSMS(sms *MsgQueueItem) (string, error) {
	lm := h.gateway.LogManager

	params := &twilioApi.CreateMessageParams{}
	params.SetTo(sms.To)
	params.SetFrom(sms.From)
	params.SetBody(sms.message)

	msg, err := h.client.Api.CreateMessage(params)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendSMS.Twilio",
			"Failed to send SMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": sms.LogID,
				"to":    sms.To,
				"from":  sms.From,
			}, err,
		))
		return "", err
	}

	// Extract message SID from response
	messageSid := ""
	if msg != nil && msg.Sid != nil {
		messageSid = *msg.Sid
	}

	return messageSid, nil
}

// SendMMS sends an MMS message via the Twilio API.
func (h *TwilioHandler) SendMMS(mms *MsgQueueItem) (string, error) {
	lm := h.gateway.LogManager

	params := &twilioApi.CreateMessageParams{}
	params.SetTo(mms.To)
	params.SetFrom(mms.From)
	params.SetBody("") // MMS body (Twilio uses empty body with media)

	var mediaUrls []string

	if len(mms.files) > 0 {
		for _, i := range mms.files {
			if strings.Contains(i.ContentType, "application/smil") {
				continue
			}

			accessToken, err := h.gateway.saveMsgFileMedia(i)
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Carrier.SendMMS.Twilio",
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
		params.MediaUrl = &mediaUrls
	}

	msg, err := h.client.Api.CreateMessage(params)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Carrier.SendMMS.Twilio",
			"Failed to send MMS to Carrier",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID": mms.LogID,
				"to":    mms.To,
				"from":  mms.From,
			}, err,
		))
		return "", err
	}

	// Extract message SID from response
	messageSid := ""
	if msg != nil && msg.Sid != nil {
		messageSid = *msg.Sid
	}

	return messageSid, nil
}

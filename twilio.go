package main

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"os"
)

// TwilioHandler implements CarrierHandler for Twilio
type TwilioHandler struct {
	BaseCarrierHandler
	client *twilio.RestClient
}

func NewTwilioHandler(logger *CustomLogger) *TwilioHandler {
	return &TwilioHandler{
		BaseCarrierHandler: BaseCarrierHandler{name: "twilio", logger: logger},
		client: twilio.NewRestClientWithParams(twilio.ClientParams{
			Username: os.Getenv("TWILIO_ACCOUNT_SID"),
			Password: os.Getenv("TWILIO_AUTH_TOKEN"),
		}),
	}
}

func (h *TwilioHandler) HandleInbound(c *fiber.Ctx, gateway *SMSGateway) error {
	sms := &SMS{
		From:    c.FormValue("From"),
		To:      c.FormValue("To"),
		Content: c.FormValue("Body"),
		CarrierData: map[string]string{
			"MessageSid": c.FormValue("MessageSid"),
			"AccountSid": c.FormValue("AccountSid"),
		},
	}

	twiml := "<?xml version=\"1.0\" encoding=\"UTF-8\"?><Response>"

	if sms.Content == "STOP" || sms.Content == "UNSUBSCRIBE" {
		err := gateway.setOptOutStatus(sms.To, sms.From, true)
		if err != nil {
			h.logger.Log(fmt.Sprintf("Error setting opt-out status: %v", err))
			return c.Status(500).SendString(fmt.Sprintf("Error setting opt-out status: %v", err))
		}
		twiml += "<Message>You have successfully opted out of messages from this sender.</Message>"
	} else if sms.Content == "START" || sms.Content == "SUBSCRIBE" {
		err := gateway.setOptOutStatus(sms.To, sms.From, false)
		if err != nil {
			h.logger.Log(fmt.Sprintf("Error setting opt-in status: %v", err))
			return c.Status(500).SendString(fmt.Sprintf("Error setting opt-in status: %v", err))
		}
		twiml += "<Message>You have successfully opted in to receive messages from this sender.</Message>"
	} else {
		// Forward the SMS to Jasmin
		// forward to SMPP conn as SMS to PBX/System
		err := SendToSmppClient(sms)
		if err != nil {
			h.logger.Log(fmt.Sprintf("Error forwarding SMS to Client: %v", err))
			return c.Status(500).SendString("Error forwarding SMS")
		}
		// todo maybe not include this if it is a normal message?
		twiml += "<Message>Your message has been received and processed.</Message>"
	}

	twiml += "</Response>"
	c.Set("Content-Type", "application/xml")
	return c.SendString(twiml)
}

func (h *TwilioHandler) HandleOutbound(c *fiber.Ctx, gateway *SMSGateway) error {
	// Parse URL-encoded parameters
	sms := SMS{
		From:    c.Query("from"),
		To:      c.Query("to"),
		Content: c.Query("content"),
		// Add other fields as necessary, e.g.:
		// ID:              c.Query("id"),
		// OriginConnector: c.Query("origin-connector"),
		// Priority:        c.Query("priority"),
		// Coding:          c.Query("coding"),
		// Validity:        c.Query("validity"),
	}

	// Validate required fields
	if sms.From == "" || sms.To == "" || sms.Content == "" {
		return c.Status(400).SendString("Missing required parameters: 'from', 'to', or 'content'")
	}

	optedOut, err := gateway.isOptedOut(sms.From, sms.To)
	if err != nil {
		h.logger.Log(fmt.Sprintf("Error checking opt-out status: %v", err))
		return c.Status(500).SendString("Error checking opt-out status")
	}

	if optedOut {
		return c.Status(403).SendString("Recipient has opted out of messages from this sender")
	}

	err = h.SendSMS(&sms)
	if err != nil {
		h.logger.Log(fmt.Sprintf("Error sending SMS: %v", err))
		return c.Status(500).SendString("Error sending SMS")
	}

	return c.SendStatus(200)
}

func (h *TwilioHandler) SendSMS(sms *SMS) error {
	params := &twilioApi.CreateMessageParams{}
	params.SetTo(sms.To)
	params.SetFrom(sms.From)
	params.SetBody(sms.Content)

	// todo support for MMS??

	_, err := h.client.Api.CreateMessage(params)
	if err != nil {
		return fmt.Errorf("error sending SMS via Twilio: %v", err)
	}

	h.logger.Log(fmt.Sprintf("SMS sent successfully via Twilio: From %s To %s", sms.From, sms.To))
	return nil
}

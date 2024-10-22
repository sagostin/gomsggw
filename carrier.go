package main

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"os"
	"strings"
)

func (h *BaseCarrierHandler) Name() string {
	return h.name
}

// BaseCarrierHandler provides common functionality for carriers
type BaseCarrierHandler struct {
	name string
}

// CarrierHandler interface for different carrier handlers
type CarrierHandler interface {
	Inbound(c *fiber.Ctx, gateway *Gateway) error
	SendSMS(sms *CarrierMessage) error
	SendMMS(sms *MM4Message) error
	Name() string
}

type CarrierConfig struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Add any carrier-specific configuration fields here
}

func loadCarriers(gateway *Gateway) error {
	var configs []CarrierConfig

	// todo load from elsewhere
	twilioEnable := os.Getenv("CARRIER_TWILIO")
	if strings.ToLower(twilioEnable) == "true" {
		configs = append(configs, CarrierConfig{
			Name: "twilio",
			Type: "twilio",
		})
	}

	telynxEnable := os.Getenv("CARRIER_TELYNX")
	if strings.ToLower(telynxEnable) == "true" {
		configs = append(configs, CarrierConfig{
			Name: "telynx",
			Type: "telynx",
		})
	}

	carriers := make(map[string]CarrierHandler)
	for _, config := range configs {
		switch config.Type {
		case "twilio":
			carriers[config.Name] = NewTwilioHandler(gateway)
		// Add cases for other carrier types here
		default:
			return fmt.Errorf("unknown carrier type: %s", config.Type)
		}
	}

	gateway.Carriers = carriers

	return nil
}

// CarrierMessage represents an SMS message for SMPP.
type CarrierMessage struct {
	From        string
	To          string
	Content     string
	CarrierData map[string]string
}

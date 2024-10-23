package main

import (
	"fmt"
	"github.com/gofiber/fiber/v2"
	"os"
	"strings"
)

/*var CarrierType = struct {
	Twilio string
	Telynx string
}{
	Twilio: "twilio",
	Telynx: "telynx",
}*/

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
	SendSMS(sms *SMPPMessage) error
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

	// todo add more?
	carrier := []string{"twilio", "telynx"}

	for _, cName := range carrier {
		if strings.ToLower(os.Getenv("CARRIER_"+strings.ToUpper(cName))) == "true" {
			configs = append(configs, CarrierConfig{
				Name: cName,
				Type: cName,
			})
		}
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

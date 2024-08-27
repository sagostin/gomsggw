package main

import (
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"io/ioutil"
)

func (h *BaseCarrierHandler) Name() string {
	return h.name
}

// CarrierHandler interface for different carrier handlers
type CarrierHandler interface {
	HandleInbound(c *fiber.Ctx, gateway *SMSGateway) error
	HandleOutbound(c *fiber.Ctx, gateway *SMSGateway) error
	SendSMS(sms *SMS) error
	Name() string
}

type CarrierConfig struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Add any carrier-specific configuration fields here
}

func loadCarriers(configPath string, logger *CustomLogger, gateway *SMSGateway) (map[string]CarrierHandler, error) {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var configs []CarrierConfig
	err = json.Unmarshal(data, &configs)
	if err != nil {
		return nil, err
	}

	carriers := make(map[string]CarrierHandler)
	for _, config := range configs {
		switch config.Type {
		case "twilio":
			carriers[config.Name] = NewTwilioHandler(logger, gateway)
		// Add cases for other carrier types here
		default:
			return nil, fmt.Errorf("unknown carrier type: %s", config.Type)
		}
	}

	return carriers, nil
}

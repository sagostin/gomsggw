package main

import (
	"fmt"
	"github.com/kataras/iris/v12"
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
	Inbound(c iris.Context, gateway *Gateway) error
	SendSMS(sms *MsgQueueItem) error
	SendMMS(sms *MsgQueueItem) error
	Name() string
}

type Carrier struct {
	Name string `json:"name"`
	// Add any carrier-specific configuration fields here
}

func loadCarriers(gateway *Gateway) error {
	var configs []Carrier

	// todo add more?
	carrier := []string{"twilio", "telnyx"}

	for _, cName := range carrier {
		if strings.ToLower(os.Getenv(strings.ToUpper(cName)+"_ENABLE")) == "true" {
			configs = append(configs, Carrier{
				Name: cName,
			})
		}
	}

	carriers := make(map[string]CarrierHandler)
	for _, config := range configs {
		switch config.Name {
		case "twilio":
			carriers[config.Name] = NewTwilioHandler(gateway)
		case "telnyx":
			carriers[config.Name] = NewTelnyxHandler(gateway)
		// Add cases for other carrier types here
		default:
			return fmt.Errorf("unknown carrier type: %s", config.Name)
		}
	}

	// Capture 'name' and 'handler' to avoid closure issues

	gateway.Carriers = carriers

	return nil
}

package main

import (
	"fmt"
	"github.com/M2MGateway/go-smpp"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"log"
	"os"
)

func main() {
	// Load environment variables
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file. Using existing environment variables.")
	}

	// Initialize Loki client
	lokiClient := NewLokiClient(
		os.Getenv("LOKI_URL"),
		os.Getenv("LOKI_USERNAME"),
		os.Getenv("LOKI_PASSWORD"),
	)

	// Create custom logger
	logger := NewCustomLogger(lokiClient)

	app := fiber.New()

	gateway, err := NewSMSGateway(os.Getenv("MONGODB_URI"), logger)
	if err != nil {
		logger.Log(fmt.Sprintf("Failed to create SMS gateway: %v", err))
		os.Exit(1)
	}

	smppServer, err := initSmppServer()
	if err != nil {
		return
	}
	carriers, err := loadCarriers("carriers.json", logger, gateway)
	if err != nil {
		logger.Log(fmt.Sprintf("Failed to load carriers: %v", err))
		os.Exit(1)
	}

	smppServer.AddRoute("1", "carrier", "twilio", carriers["twilio"])
	gateway.Carriers = carriers
	gateway.SmppServer = smppServer

	for name, handler := range gateway.Carriers {
		inboundPath := fmt.Sprintf("/inbound/%s", name)

		app.Post(inboundPath, func(c *fiber.Ctx) error {
			return handler.HandleInbound(c, gateway)
		})
	}

	// Start server
	webListen := os.Getenv("WEB_LISTEN")
	if webListen == "" {
		webListen = "0.0.0.0:3000"
	}

	handler := NewSimpleHandler(gateway.SmppServer)
	smppListen := os.Getenv("SMPP_LISTEN")
	if smppListen == "" {
		smppListen = "0.0.0.0:2775"
	}

	go func() {
		log.Printf("Starting SMPP server on %s", smppListen)
		err = smpp.ServeTCP(smppListen, handler, nil)
		if err != nil {
			log.Fatalf("Error serving SMPP: %v", err)
		}
	}()

	go func() {
		smppServer.handleInboundSMS()
	}()

	go func() {
		smppServer.handleOutboundSMS()
	}()

	err = app.Listen(webListen)
	if err != nil {
		log.Println(err)
	}
}

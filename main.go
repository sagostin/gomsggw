package main

import (
	"fmt"
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

	carriers, err := loadCarriers("carriers.json", logger, gateway)
	if err != nil {
		logger.Log(fmt.Sprintf("Failed to load carriers: %v", err))
		os.Exit(1)
	}

	gateway.Routing = &Routing{Routes: make([]*Route, 0)}

	gateway.Routing.AddRoute("1", "carrier", "twilio", carriers["twilio"])

	go func() {
		smppServer, err := initSmppServer()
		if err != nil {
			return
		}
		smppServer.routing = gateway.Routing
		gateway.Carriers = carriers
		gateway.SMPPServer = smppServer

		smppServer.Start(gateway)
	}()

	go func() {
		mm4Server := MM4Server{
			Addr:  os.Getenv("MM4_LISTEN"),
			mongo: gateway.MongoClient,
		}
		err := mm4Server.Start()
		if err != nil {
			return
		}
	}()

	for name, handler := range gateway.Carriers {
		inboundPath := fmt.Sprintf("/inbound/%s", name)

		app.Post(inboundPath, func(c *fiber.Ctx) error {
			return handler.Inbound(c, gateway)
		})
	}

	// Start server
	webListen := os.Getenv("WEB_LISTEN")
	if webListen == "" {
		webListen = "0.0.0.0:3000"
	}

	err = app.Listen(webListen)
	if err != nil {
		log.Println(err)
	}
}

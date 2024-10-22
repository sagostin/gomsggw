package main

import (
	"encoding/base64"
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

	gateway, err := NewGateway(os.Getenv("MONGODB_URI"), logger)
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
		mm4Server := &MM4Server{
			Addr:    os.Getenv("MM4_LISTEN"),
			mongo:   gateway.MongoClient,
			routing: gateway.Routing,
		}
		gateway.MM4Server = mm4Server
		err := mm4Server.Start()
		if err != nil {
			print("error starting mm4")
			return
		}
	}()

	//todo support multiple
	inboundPath := fmt.Sprintf("/inbound/twilio")
	// Capture 'name' and 'handler' to avoid closure issues
	app.Post(inboundPath, func(c *fiber.Ctx) error {
		return carriers["twilio"].Inbound(c, gateway)
	})

	// Define the GET /media/:id route
	app.Get("/media/:id", func(c *fiber.Ctx) error {
		fileID := c.Params("id")
		if fileID == "" {
			return fiber.NewError(fiber.StatusBadRequest, "File ID is required")
		}

		// Retrieve the media file from MongoDB
		mediaFile, err := getMediaFromMongoDB(gateway.MongoClient, fileID)
		if err != nil {
			// Return 404 if the file is not found or any other retrieval error occurs
			return fiber.NewError(fiber.StatusNotFound, err.Error())
		}

		// Decode the Base64-encoded data
		fileBytes, err := base64.StdEncoding.DecodeString(mediaFile.Base64Data)
		if err != nil {
			// Return 500 if decoding fails
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to decode file data")
		}

		// Set the appropriate Content-Type header
		c.Set("Content-Type", mediaFile.ContentType)

		// Optionally, set Content-Disposition to suggest a filename for download
		// Uncomment the following line if you want the browser to prompt a download
		// c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", mediaFile.FileName))

		// Send the file bytes as the response
		return c.Send(fileBytes)
	})

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

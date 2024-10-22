package main

import (
	"encoding/base64"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"log"
	"os"
	"strings"
)

func main() {
	logf := LoggingFormat{Path: "main", Function: "main"}

	// Load environment variables
	err := godotenv.Load()
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
	}

	app := fiber.New()

	gateway, err := NewGateway(os.Getenv("MONGODB_URI"))
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "Failed to create gateway"
		logf.Print()
		os.Exit(1)
	}

	err = loadCarriers(gateway)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "Failed to load carriers"
		logf.Print()
		os.Exit(1)
	}

	go func() {
		smppServer, err := initSmppServer()
		if err != nil {
			return
		}
		smppServer.routing = gateway.Routing
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
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = "Failed to create MM4 server"
			logf.Print()
			os.Exit(1)
		}
	}()

	//todo support multiple & load on the fly
	twilioEnable := os.Getenv("CARRIER_TWILIO")
	if strings.ToLower(twilioEnable) == "true" {
		inboundPath := fmt.Sprintf("/inbound/twilio")
		// Capture 'name' and 'handler' to avoid closure issues
		app.Post(inboundPath, func(c *fiber.Ctx) error {
			return gateway.Carriers["twilio"].Inbound(c, gateway)
		})

		gateway.Routing.AddRoute("carrier", "twilio", gateway.Carriers["twilio"])
	}

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

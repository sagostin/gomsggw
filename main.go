package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
)

// LokiClient holds the configuration for the Loki client.
type LokiClient struct {
	PushURL  string // URL to Loki's push API
	Username string // Username for basic auth
	Password string // Password for basic auth
}

// Update the LogEntry struct to use time.Time for Timestamp
type LogEntry struct {
	Timestamp time.Time
	Line      string
}

// LokiPushData represents the data structure required by Loki's push API.
type LokiPushData struct {
	Streams []LokiStream `json:"streams"`
}

// LokiStream represents a stream of logs with the same labels in Loki.
type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // Array of [timestamp, line] tuples
}

// NewLokiClient creates a new client to interact with Loki.
func NewLokiClient(pushURL, username, password string) *LokiClient {
	return &LokiClient{
		PushURL:  pushURL,
		Username: username,
		Password: password,
	}
}

// PushLog sends a log entry to Loki.
func (c *LokiClient) PushLog(labels map[string]string, entry LogEntry) error {
	// Convert time to Unix epoch in nanoseconds
	unixNano := entry.Timestamp.UnixNano()
	timestampStr := strconv.FormatInt(unixNano, 10)

	// Ensure the log line is properly escaped for JSON
	escapedLine := strconv.Quote(entry.Line)
	// Remove the surrounding quotes added by Quote
	escapedLine = escapedLine[1 : len(escapedLine)-1]

	payload := LokiPushData{
		Streams: []LokiStream{
			{
				Stream: labels,
				Values: [][2]string{{timestampStr, escapedLine}},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling json: %w", err)
	}

	req, err := http.NewRequest("POST", c.PushURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to Loki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received unexpected response status: %d", resp.StatusCode)
	}

	return nil
}

// CustomLogger wraps both standard logger and Loki client
type CustomLogger struct {
	stdLogger  *log.Logger
	lokiClient *LokiClient
}

// NewCustomLogger creates a new CustomLogger
func NewCustomLogger(lokiClient *LokiClient) *CustomLogger {
	return &CustomLogger{
		stdLogger:  log.New(os.Stdout, "", log.LstdFlags),
		lokiClient: lokiClient,
	}
}

// Log sends a log message to both stdout and Loki
func (l *CustomLogger) Log(message string) {
	l.stdLogger.Println(message)

	entry := LogEntry{
		Timestamp: time.Now(),
		Line:      message,
	}

	labels := map[string]string{"job": "sms-gateway"}
	err := l.lokiClient.PushLog(labels, entry)
	if err != nil {
		l.stdLogger.Printf("Error sending log to Loki: %v", err)
	}
}

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

	carriers, err := loadCarriers("carriers.json", logger)
	if err != nil {
		logger.Log(fmt.Sprintf("Failed to load carriers: %v", err))
		os.Exit(1)
	}
	gateway.Carriers = carriers

	for name, handler := range gateway.Carriers {
		inboundPath := fmt.Sprintf("/inbound/%s", name)
		outboundPath := fmt.Sprintf("/outbound/%s", name)

		app.Post(inboundPath, func(c *fiber.Ctx) error {
			return handler.HandleInbound(c, gateway)
		})
		app.Post(outboundPath, func(c *fiber.Ctx) error {
			return handler.HandleOutbound(c, gateway)
		})
	}

	// Start server
	port := os.Getenv("LISTEN")
	if port == "" {
		port = "0.0.0.0:3000"
	}

	log.Println("Started SMS Carrier GW")
	err = app.Listen(port)
	if err != nil {
		log.Println(err)
	}
}

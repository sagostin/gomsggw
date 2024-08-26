package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// LokiClient holds the configuration for the Loki client.
type LokiClient struct {
	PushURL  string // URL to Loki's push API
	Username string // Username for basic auth
	Password string // Password for basic auth
}

// LogEntry represents a single log entry.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Line      string `json:"line"`
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
	payload := LokiPushData{
		Streams: []LokiStream{
			{
				Stream: labels,
				Values: [][2]string{{entry.Timestamp, entry.Line}},
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 response status: %d", resp.StatusCode)
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
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Line:      message,
	}

	labels := map[string]string{"app": "sms-gateway"}
	err := l.lokiClient.PushLog(labels, entry)
	if err != nil {
		l.stdLogger.Printf("Error sending log to Loki: %v", err)
	}
}

// SMS represents a generic SMS message
type SMS struct {
	ID          string            `json:"id" bson:"id"`
	From        string            `json:"from" bson:"from"`
	To          string            `json:"to" bson:"to"`
	Content     string            `json:"content" bson:"content"`
	CarrierData map[string]string `json:"carrier_data" bson:"carrier_data"`
}

// OptOutStatus represents the opt-out status for a sender-receiver pair
type OptOutStatus struct {
	Sender    string    `bson:"sender"`
	Receiver  string    `bson:"receiver"`
	OptedOut  bool      `bson:"opted_out"`
	Timestamp time.Time `bson:"timestamp"`
}

// CarrierHandler interface for different carrier handlers
type CarrierHandler interface {
	HandleInbound(c *fiber.Ctx, gateway *SMSGateway) error
	HandleOutbound(c *fiber.Ctx, gateway *SMSGateway) error
	SendSMS(sms *SMS) error
	Name() string
}

// SMSGateway handles SMS processing for different carriers
type SMSGateway struct {
	Carriers         map[string]CarrierHandler
	MongoClient      *mongo.Client
	OptOutCollection *mongo.Collection
	Logger           *CustomLogger
}

// NewSMSGateway creates a new SMSGateway instance
func NewSMSGateway(mongoURI string, logger *CustomLogger) (*SMSGateway, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, err
	}

	database := client.Database("sms_gateway")
	optOutCollection := database.Collection("opt_out_status")

	// Create a compound index on sender and receiver
	_, err = optOutCollection.Indexes().CreateOne(
		context.Background(),
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "sender", Value: 1},
				{Key: "receiver", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
	)
	if err != nil {
		return nil, err
	}

	return &SMSGateway{
		Carriers:         make(map[string]CarrierHandler),
		MongoClient:      client,
		OptOutCollection: optOutCollection,
		Logger:           logger,
	}, nil
}

func (g *SMSGateway) isOptedOut(sender, receiver string) (bool, error) {
	var status OptOutStatus
	err := g.OptOutCollection.FindOne(
		context.Background(),
		bson.M{"sender": sender, "receiver": receiver},
	).Decode(&status)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil // Not in DB, so not opted out
	} else if err != nil {
		return false, err
	}
	return status.OptedOut, nil
}

func (g *SMSGateway) setOptOutStatus(sender, receiver string, optOut bool) error {
	_, err := g.OptOutCollection.UpdateOne(
		context.Background(),
		bson.M{"sender": sender, "receiver": receiver},
		bson.M{"$set": bson.M{"opted_out": optOut, "timestamp": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// BaseCarrierHandler provides common functionality for carriers
type BaseCarrierHandler struct {
	name   string
	logger *CustomLogger
}

func (h *BaseCarrierHandler) Name() string {
	return h.name
}

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
		err := forwardToJasmin(sms)
		if err != nil {
			h.logger.Log(fmt.Sprintf("Error forwarding SMS to Jasmin: %v", err))
			return c.Status(500).SendString("Error forwarding SMS")
		}
		twiml += "<Message>Your message has been received and processed.</Message>"
	}

	twiml += "</Response>"
	c.Set("Content-Type", "application/xml")
	return c.SendString(twiml)
}

func (h *TwilioHandler) HandleOutbound(c *fiber.Ctx, gateway *SMSGateway) error {
	var sms SMS
	if err := c.BodyParser(&sms); err != nil {
		return c.Status(400).SendString("Invalid request body")
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

	_, err := h.client.Api.CreateMessage(params)
	if err != nil {
		return fmt.Errorf("error sending SMS via Twilio: %v", err)
	}

	h.logger.Log(fmt.Sprintf("SMS sent successfully via Twilio: From %s To %s", sms.From, sms.To))
	return nil
}

func forwardToJasmin(sms *SMS) error {
	// Implement logic to forward SMS to Jasmin
	// This is a placeholder and should be replaced with actual Jasmin API calls
	log.Printf("Forwarding to Jasmin: From %s To %s: %s", sms.From, sms.To, sms.Content)
	return nil
}

type CarrierConfig struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Add any carrier-specific configuration fields here
}

func loadCarriers(configPath string, logger *CustomLogger) (map[string]CarrierHandler, error) {
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
			carriers[config.Name] = NewTwilioHandler(logger)
		// Add cases for other carrier types here
		default:
			return nil, fmt.Errorf("unknown carrier type: %s", config.Type)
		}
	}

	return carriers, nil
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
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
}

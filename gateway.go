package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// GatewayConfig holds global configuration defaults
type GatewayConfig struct {
	// Web client defaults (can be overridden per-client)
	WebhookRetries        int `json:"webhook_retries"`          // Default: 3
	WebhookTimeoutSecs    int `json:"webhook_timeout_secs"`     // Default: 10
	WebhookRetryDelaySecs int `json:"webhook_retry_delay_secs"` // Default: 5

	// SMPP (SMS) defaults
	SMPPRetries     int `json:"smpp_retries"`      // Default: 3
	SMPPTimeoutSecs int `json:"smpp_timeout_secs"` // Default: 30

	// MM4 (MMS) defaults
	MM4Retries     int `json:"mm4_retries"`      // Default: 3
	MM4TimeoutSecs int `json:"mm4_timeout_secs"` // Default: 60

	// Failure notification
	NotifySenderOnFailure bool `json:"notify_sender_on_failure"` // Send error back to original sender
}

// Gateway handles SMS processing for different carriers
type Gateway struct {
	Config       GatewayConfig
	Carriers     map[string]CarrierHandler
	CarrierUUIDs map[string]Carrier
	DB           *gorm.DB
	SMPPServer   *SMPPServer
	Router       *Router
	MM4Server    *MM4Server
	//AMPQClient    *AMPQClient
	Clients       map[string]*Client
	Numbers       map[string]*ClientNumber
	LogManager    *LogManager
	mu            sync.RWMutex
	MsgRecordChan chan MsgRecord
	ServerID      string
	EncryptionKey string // PSK for encryption/decryption
	// AckTracker for carrier acknowledgments.
	ConvoManager *ConvoManager
}

type MsgRecord struct {
	MsgQueueItem MsgQueueItem
	ClientID     uint
	Carrier      string
	Internal     bool

	// Enhanced tracking
	Direction      string // "inbound" or "outbound"
	FromClientType string // "legacy", "web", or "carrier"
	ToClientType   string // "legacy", "web", or "carrier"
	DeliveryMethod string // "smpp", "mm4", "webhook", "carrier_api"
	Encoding       string // For SMS: "gsm7", "ucs2", etc.

	// Segment tracking for split messages (all segments share same LogID)
	TotalSegments int // Total number of segments in this message (1 for single-part)
	SegmentIndex  int // Index of this segment (0-based)

	// MMS transcoding
	OriginalSizeBytes    int
	TranscodedSizeBytes  int
	MediaCount           int
	TranscodingPerformed bool
}

func getPostgresDSN() string {
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		host = "localhost"
	}

	port := os.Getenv("POSTGRES_PORT")
	if port == "" {
		port = "5432"
	}

	user := os.Getenv("POSTGRES_USER")
	password := os.Getenv("POSTGRES_PASSWORD")
	dbName := os.Getenv("POSTGRES_DB")
	sslMode := os.Getenv("POSTGRES_SSLMODE")
	if sslMode == "" {
		sslMode = "disable"
	}

	timeZone := os.Getenv("POSTGRES_TIMEZONE")
	if timeZone == "" {
		timeZone = "America/Vancouver"
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		host, port, user, password, dbName, sslMode, timeZone,
	)

	return dsn
}

// loadGatewayConfig loads global configuration from environment variables
func loadGatewayConfig() GatewayConfig {
	config := GatewayConfig{
		WebhookRetries:        3,
		WebhookTimeoutSecs:    10,
		WebhookRetryDelaySecs: 5,
		SMPPRetries:           3,
		SMPPTimeoutSecs:       30,
		MM4Retries:            3,
		MM4TimeoutSecs:        60,
		NotifySenderOnFailure: true,
	}

	if val := os.Getenv("WEBHOOK_RETRIES"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.WebhookRetries = v
		}
	}
	if val := os.Getenv("WEBHOOK_TIMEOUT_SECS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.WebhookTimeoutSecs = v
		}
	}
	if val := os.Getenv("WEBHOOK_RETRY_DELAY_SECS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.WebhookRetryDelaySecs = v
		}
	}
	if val := os.Getenv("SMPP_RETRIES"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.SMPPRetries = v
		}
	}
	if val := os.Getenv("SMPP_TIMEOUT_SECS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.SMPPTimeoutSecs = v
		}
	}
	if val := os.Getenv("MM4_RETRIES"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.MM4Retries = v
		}
	}
	if val := os.Getenv("MM4_TIMEOUT_SECS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.MM4TimeoutSecs = v
		}
	}
	if val := os.Getenv("NOTIFY_SENDER_ON_FAILURE"); val != "" {
		config.NotifySenderOnFailure = strings.ToLower(val) == "true" || val == "1"
	}

	return config
}

// NewGateway creates a new Gateway instance
func NewGateway() (*Gateway, error) {
	// Load environment variables or configuration for the database
	dsn := getPostgresDSN() // e.g., "host=localhost user=postgres password=yourpassword dbname=yourdb port=5432 sslmode=disable TimeZone=Asia/Shanghai"

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %v", err)
	}

	gateway := &Gateway{
		Config:       loadGatewayConfig(),
		Carriers:     make(map[string]CarrierHandler),
		CarrierUUIDs: make(map[string]Carrier),
		Router: &Router{
			Routes:         make([]*Route, 0),
			ClientMsgChan:  make(chan MsgQueueItem),
			CarrierMsgChan: make(chan MsgQueueItem),
		},
		MsgRecordChan: make(chan MsgRecord),
		Clients:       make(map[string]*Client),
		Numbers:       make(map[string]*ClientNumber),
		ServerID:      os.Getenv("SERVER_ID"),
		DB:            db,
	}

	gateway.ConvoManager = NewConvoManager()

	gateway.Router.gateway = gateway

	// Initialize Loki Client and Log Manager
	lokiClient := NewLokiClient(os.Getenv("LOKI_URL"), os.Getenv("LOKI_USERNAME"), os.Getenv("LOKI_PASSWORD"))
	lokiEnabled := strings.ToLower(os.Getenv("LOKI_ENABLED")) == "true" || os.Getenv("LOKI_ENABLED") == "1"
	logManager := NewLogManager(lokiClient, lokiEnabled)
	// Define Templates
	logManager.LoadTemplates()
	gateway.LogManager = logManager

	// Migrate the schema
	if err := gateway.migrateSchema(); err != nil {
		return nil, err
	}

	// Load clients and numbers from the database
	if err := gateway.loadClients(); err != nil {
		return nil, fmt.Errorf("failed to load clients: %v", err)
	}

	if err := gateway.loadNumbers(); err != nil {
		return nil, fmt.Errorf("failed to load numbers: %v", err)
	}

	return gateway, nil
}

func (gateway *Gateway) getCarrier(number string) (string, error) {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	num, exists := gateway.Numbers[number]
	if !exists {
		return "", fmt.Errorf("no carrier found for number: %s", number)
	}
	return num.Carrier, nil
}

// getClient returns the client associated with a phone number.
func (gateway *Gateway) getClient(number string) *Client {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	for _, client := range gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return client
			}
		}
	}
	return nil
}

// getClient returns the client associated with a phone number.
func (gateway *Gateway) getNumber(number string) *ClientNumber {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	for _, client := range gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return &num
			}
		}
	}
	return nil
}

func (gateway *Gateway) getClientCarrier(number string) (string, error) {
	for _, client := range gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return num.Carrier, nil
			}
		}
	}

	return "", nil
}

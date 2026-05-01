package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
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
	APIKeys       map[string]*TenantAPIKey // Keyed by SHA-256 hash of raw key
	LogManager    *LogManager
	mu            sync.RWMutex
	MsgRecordChan chan MsgRecord
	ServerID      string
	EncryptionKey string // PSK for encryption/decryption
	// AckTracker for carrier acknowledgments.
	ConvoManager *ConvoManager
	// CommandQueue buffers events when cloud is unreachable.
	CommandQueue *CommandQueue
	// CloudStatus tracks whether the cloud connection is available.
	CloudStatus CloudStatus
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
	SourceIP       string // Originating IP address (for web/API messages)

	// SMS tracking
	TotalSegments       int // Total number of segments in this message (1 for single-part)
	OriginalBytesLength int // Original message byte length

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
		APIKeys:       make(map[string]*TenantAPIKey),
		ServerID:      os.Getenv("SERVER_ID"),
		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),
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

	// Load API keys into memory
	if err := gateway.loadAPIKeys(); err != nil {
		return nil, fmt.Errorf("failed to load API keys: %v", err)
	}

	// Initialize the command queue for offline buffering
	if err := gateway.initCommandQueue(); err != nil {
		return nil, fmt.Errorf("failed to initialize command queue: %v", err)
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

// resolveFailoverSession finds the first active fallback SMPP session for a client
// whose primary session is offline or failed. Returns the session, the fallback client
// that owns the session, and any error. Failovers are tried in priority order (lowest first).
func (gateway *Gateway) resolveFailoverSession(primaryClient *Client) (*Client, error) {
	if primaryClient == nil || len(primaryClient.Failovers) == 0 {
		return nil, fmt.Errorf("no failovers configured for client %s", primaryClient.Username)
	}

	lm := gateway.LogManager

	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	for _, failover := range primaryClient.Failovers {
		// Find the fallback client in memory
		var fallbackClient *Client
		for _, c := range gateway.Clients {
			if c.ID == failover.FallbackClientID {
				fallbackClient = c
				break
			}
		}

		if fallbackClient == nil {
			lm.SendLog(lm.BuildLog(
				"Gateway.Failover",
				"FallbackClientNotFound",
				logrus.WarnLevel,
				map[string]interface{}{
					"primaryClient":    primaryClient.Username,
					"fallbackClientID": failover.FallbackClientID,
					"priority":         failover.Priority,
				},
			))
			continue
		}

		// Check if the fallback client has an active SMPP session
		if gateway.SMPPServer.isSessionActive(fallbackClient.Username) {
			lm.SendLog(lm.BuildLog(
				"Gateway.Failover",
				"FailoverActivated",
				logrus.InfoLevel,
				map[string]interface{}{
					"primaryClient":  primaryClient.Username,
					"fallbackClient": fallbackClient.Username,
					"priority":       failover.Priority,
				},
			))
			return fallbackClient, nil
		}

		lm.SendLog(lm.BuildLog(
			"Gateway.Failover",
			"FallbackClientOffline",
			logrus.WarnLevel,
			map[string]interface{}{
				"primaryClient":  primaryClient.Username,
				"fallbackClient": fallbackClient.Username,
				"priority":       failover.Priority,
			},
		))
	}

	return nil, fmt.Errorf("all failover clients offline for %s", primaryClient.Username)
}

// initCommandQueue initializes the SQLite-backed command queue.
func (gateway *Gateway) initCommandQueue() error {
	dbPath := os.Getenv("COMMAND_QUEUE_DB")
	if dbPath == "" {
		dbPath = "/var/lib/gomsggw/command_queue.db"
	}

	q, err := NewCommandQueue(dbPath)
	if err != nil {
		return err
	}

	// Apply max size from env (default 100MB)
	if maxSize := os.Getenv("COMMAND_QUEUE_MAX_SIZE_MB"); maxSize != "" {
		if mb, err := strconv.ParseInt(maxSize, 10, 64); err == nil {
			q.SetMaxSize(mb * 1024 * 1024)
		}
	}

	gateway.CommandQueue = q
	gateway.CloudStatus = CloudStatusOnline
	return nil
}

// SetCloudStatus updates the cloud connection status and triggers a flush if coming back online.
func (gateway *Gateway) SetCloudStatus(status CloudStatus) {
	if gateway.CloudStatus == CloudStatusOffline && status == CloudStatusOnline {
		// Cloud reconnected — trigger queue flush
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"CommandQueue", "CloudReconnected", logrus.InfoLevel,
			map[string]interface{}{}, nil,
		))
		go gateway.flushCommandQueue()
	}
	gateway.CloudStatus = status
}

// IsCloudOnline returns true if the cloud connection is available.
func (gateway *Gateway) IsCloudOnline() bool {
	return gateway.CloudStatus == CloudStatusOnline
}

// flushCommandQueue drains the command queue by sending items to the router in order.
func (gateway *Gateway) flushCommandQueue() {
	ctx := context.Background()
	items, err := gateway.CommandQueue.FlushAll(ctx)
	if err != nil {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"CommandQueue", "FlushError", logrus.ErrorLevel,
			map[string]interface{}{"error": err.Error()}, err,
		))
		return
	}

	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"CommandQueue", "FlushStarted", logrus.InfoLevel,
		map[string]interface{}{"item_count": len(items)}, nil,
	))

	for _, item := range items {
		var msg MsgQueueItem
		if err := json.Unmarshal([]byte(item.Payload), &msg); err != nil {
			gateway.CommandQueue.MarkFailed(ctx, item.ID, item.RetryCount+1)
			continue
		}

		// Route the message — use CarrierMsgChan for outbound carrier sends
		gateway.Router.CarrierMsgChan <- msg

		// Wait briefly between items to avoid overwhelming the router
		time.Sleep(10 * time.Millisecond)
	}
}

// EnqueueEvent persists a MsgQueueItem to the command queue when cloud is offline.
// eventType is "sms" or "mms". Returns the queued event ID and any error.
func (gateway *Gateway) EnqueueEvent(ctx context.Context, item *MsgQueueItem, eventType string) (string, error) {
	if gateway.CommandQueue == nil {
		return "", fmt.Errorf("command queue not initialized")
	}
	return gateway.CommandQueue.Enqueue(ctx, item, eventType)
}

// SendEventToCarrier attempts to send a message to a carrier. If the cloud is offline,
// the message is persisted to the command queue and returns immediately.
// This implements the offline-first strategy: queue locally, ACK to PMS, resume on reconnect.
func (gateway *Gateway) SendEventToCarrier(ctx context.Context, item *MsgQueueItem, eventType string) error {
	if gateway.CloudStatus != CloudStatusOnline {
		// Cloud offline: persist to SQLite queue and return success
		// PMS receives ACK immediately; message will be delivered on reconnect
		_, err := gateway.EnqueueEvent(ctx, item, eventType)
		return err
	}

	// Cloud online: process normally through the router
	// Enqueue to carrier channel for processing
	gateway.Router.CarrierMsgChan <- *item
	return nil
}


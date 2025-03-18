package main

import (
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"os"
	"strings"
	"sync"
)

// Gateway handles SMS processing for different carriers
type Gateway struct {
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
}

type MsgRecord struct {
	MsgQueueItem MsgQueueItem
	ClientID     uint
	Carrier      string
	Internal     bool
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

// NewGateway creates a new Gateway instance
func NewGateway() (*Gateway, error) {
	// Load environment variables or configuration for the database
	dsn := getPostgresDSN() // e.g., "host=localhost user=postgres password=yourpassword dbname=yourdb port=5432 sslmode=disable TimeZone=Asia/Shanghai"

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %v", err)
	}

	gateway := &Gateway{
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

	gateway.Router.gateway = gateway

	// Initialize Loki Client and Log Manager
	lokiClient := NewLokiClient(os.Getenv("LOKI_URL"), os.Getenv("LOKI_USERNAME"), os.Getenv("LOKI_PASSWORD"))
	logManager := NewLogManager(lokiClient)
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

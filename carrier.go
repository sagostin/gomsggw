// carrier.go
package main

import (
	"fmt"
	"github.com/kataras/iris/v12"
	"strings"
)

// Carrier represents a messaging carrier in the database.
type Carrier struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"unique;not null" json:"name"` // e.g., "twilio", "telnyx"
	Type     string `gorm:"not null" json:"type"`        // e.g., "twilio", "telnyx"
	Username string `gorm:"not null" json:"username"`    // e.g., Account SID for Twilio (encrypted)
	Password string `gorm:"not null" json:"password"`    // e.g., Auth Token for Twilio (encrypted)
	// Add any carrier-specific configuration fields here
}

// Name returns the name of the carrier handler
func (h *BaseCarrierHandler) Name() string {
	return h.name
}

// BaseCarrierHandler provides common functionality for carriers
type BaseCarrierHandler struct {
	name string
}

// CarrierHandler interface for different carrier handlers
type CarrierHandler interface {
	Inbound(c iris.Context) error
	SendSMS(sms *MsgQueueItem) error
	SendMMS(sms *MsgQueueItem) error
	Name() string
}

// loadCarriers loads carriers from the database and initializes their handlers.
func (gateway *Gateway) loadCarriers() error {
	var carriers []Carrier

	// Fetch all carriers from the database
	if err := gateway.DB.Find(&carriers).Error; err != nil {
		return err
	}

	carriersMap := make(map[string]CarrierHandler)

	// Initialize carrier handlers based on their type
	for _, carrier := range carriers {
		// Decrypt sensitive fields
		decryptedUsername, err := DecryptPassword(carrier.Username, gateway.EncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt username for carrier %s: %w", carrier.Name, err)
		}
		decryptedPassword, err := DecryptPassword(carrier.Password, gateway.EncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt password for carrier %s: %w", carrier.Name, err)
		}

		var handler CarrierHandler
		switch strings.ToLower(carrier.Type) {
		case "twilio":
			handler = NewTwilioHandler(gateway, &carrier, decryptedUsername, decryptedPassword)
		case "telnyx":
			handler = NewTelnyxHandler(gateway, &carrier, decryptedUsername, decryptedPassword)
		// Add cases for other carrier types here
		default:
			return fmt.Errorf("unknown carrier type: %s", carrier.Type)
		}
		carriersMap[carrier.Name] = handler
	}

	// Update the Gateway's Carriers map
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.Carriers = carriersMap

	return nil
}

// addCarrier adds a new carrier to the database and initializes its handler.
func (gateway *Gateway) addCarrier(carrier *Carrier) error {
	// Encrypt sensitive fields
	encryptedUsername, err := EncryptPassword(carrier.Username, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt username: %w", err)
	}
	encryptedPassword, err := EncryptPassword(carrier.Password, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	carrier.Username = encryptedUsername
	carrier.Password = encryptedPassword

	// Create the carrier in the database
	if err := gateway.DB.Create(carrier).Error; err != nil {
		return err
	}

	// Decrypt fields for handler initialization
	decryptedUsername, err := DecryptPassword(carrier.Username, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt username after encryption: %w", err)
	}
	decryptedPassword, err := DecryptPassword(carrier.Password, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt password after encryption: %w", err)
	}

	// Initialize the carrier handler based on its type
	var handler CarrierHandler
	switch strings.ToLower(carrier.Type) {
	case "twilio":
		handler = NewTwilioHandler(gateway, carrier, decryptedUsername, decryptedPassword)
	case "telnyx":
		handler = NewTelnyxHandler(gateway, carrier, decryptedUsername, decryptedPassword)
	// Add cases for other carrier types here
	default:
		return fmt.Errorf("unknown carrier type: %s", carrier.Type)
	}

	// Add the handler to the in-memory map
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.Carriers[carrier.Name] = handler

	return nil
}

// reloadCarriers reloads carriers from the database and reinitializes their handlers.
func (gateway *Gateway) reloadCarriers() error {
	return gateway.loadCarriers()
}

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type Client struct {
	ID         uint            `gorm:"primaryKey" json:"id"`
	Username   string          `gorm:"unique;not null" json:"username"`
	Password   string          `gorm:"not null" json:"-"` // Never returned in JSON
	Address    string          `json:"address"`           // IP address or hostname (required for legacy)
	Name       string          `json:"name"`
	Type       string          `json:"type" gorm:"default:'legacy'"`  // 'legacy' or 'web'
	Timezone   string          `json:"timezone" gorm:"default:'UTC'"` // IANA timezone for limit period calculation
	LogPrivacy bool            `json:"log_privacy"`
	Settings   *ClientSettings `gorm:"foreignKey:ClientID" json:"settings,omitempty"`
	Numbers    []ClientNumber  `gorm:"foreignKey:ClientID" json:"numbers"`
}

// ClientSettings contains all client configuration (applies to all client types)
type ClientSettings struct {
	ID       uint `gorm:"primaryKey" json:"id"`
	ClientID uint `gorm:"uniqueIndex;not null" json:"client_id"`

	// === Auth & API Format ===
	AuthMethod string `json:"auth_method" gorm:"default:'basic'"`  // 'basic' or 'bearer'
	APIFormat  string `json:"api_format" gorm:"default:'generic'"` // 'generic' or 'bicom'

	// === Web-specific settings (only applies to web clients) ===
	DisableMessageSplitting bool   `json:"disable_message_splitting"` // Deliver long messages as single payload (web→web only)
	WebhookRetries          int    `json:"webhook_retries"`           // Retry attempts for webhook delivery (default: 3)
	WebhookTimeoutSecs      int    `json:"webhook_timeout_secs"`      // Webhook request timeout in seconds (default: 10)
	IncludeRawSegments      bool   `json:"include_raw_segments"`      // Include individual segments in webhook payload
	DefaultWebhook          string `json:"default_webhook"`           // Fallback webhook URL (also receives ACKs)

	// === SMS Limits (applies to all client types) ===
	SMSBurstLimit   int64 `json:"sms_burst_limit"`   // Per minute (0 = unlimited)
	SMSDailyLimit   int64 `json:"sms_daily_limit"`   // Per day (0 = unlimited)
	SMSMonthlyLimit int64 `json:"sms_monthly_limit"` // Per month (0 = unlimited)

	// === MMS Limits ===
	MMSBurstLimit   int64 `json:"mms_burst_limit"`   // Per minute (0 = unlimited)
	MMSDailyLimit   int64 `json:"mms_daily_limit"`   // Per day (0 = unlimited)
	MMSMonthlyLimit int64 `json:"mms_monthly_limit"` // Per month (0 = unlimited)

	// === Limit Behavior ===
	LimitBoth bool `json:"limit_both" gorm:"default:false"` // If true, limit applies to inbound+outbound; default=false (outbound only)
}

type ClientNumber struct {
	ID                   uint            `gorm:"primaryKey" json:"id"`
	ClientID             uint            `gorm:"index;not null" json:"client_id"`
	Number               string          `gorm:"unique;not null" json:"number"`
	Carrier              string          `json:"carrier"`
	Tag                  string          `json:"tag"`   // For organizational purposes
	Group                string          `json:"group"` // For number groupings
	IgnoreStopCmdSending bool            `json:"ignore_stop_cmd_sending" gorm:"default:false;not null"`
	WebHook              string          `json:"webhook"` // Number-specific webhook URL
	Settings             *NumberSettings `gorm:"foreignKey:NumberID" json:"settings,omitempty"`
}

// NumberSettings contains per-number configuration that overrides ClientSettings
type NumberSettings struct {
	ID       uint `gorm:"primaryKey" json:"id"`
	NumberID uint `gorm:"uniqueIndex;not null" json:"number_id"`

	// === SMS Limits (0 = use client setting) ===
	SMSBurstLimit   int64 `json:"sms_burst_limit"`
	SMSDailyLimit   int64 `json:"sms_daily_limit"`
	SMSMonthlyLimit int64 `json:"sms_monthly_limit"`

	// === MMS Limits (0 = use client setting) ===
	MMSBurstLimit   int64 `json:"mms_burst_limit"`
	MMSDailyLimit   int64 `json:"mms_daily_limit"`
	MMSMonthlyLimit int64 `json:"mms_monthly_limit"`

	// === Limit Behavior ===
	LimitBoth bool `json:"limit_both" gorm:"default:false"` // Override: apply to inbound too
}

// getClientByID finds a client by its numeric ID (thread-safe)
func (gateway *Gateway) getClientByID(id uint) *Client {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	for _, client := range gateway.Clients {
		if client.ID == id {
			return client
		}
	}
	return nil
}

// LimitCheckResult contains the result of a message limit check
type LimitCheckResult struct {
	Allowed      bool   `json:"allowed"`       // Whether the message is allowed
	LimitType    string `json:"limit_type"`    // "burst", "daily", "monthly" + "_sms"/"_mms" + "_number"/"_client"
	CurrentUsage int64  `json:"current_usage"` // Current usage count
	Limit        int64  `json:"limit"`         // The limit that was exceeded
	Period       string `json:"period"`        // "burst", "daily", "monthly"
	Number       string `json:"number"`        // The number (if number-level limit)
	Message      string `json:"message"`       // User-friendly error message
}

// getEffectiveLimit returns the effective limit for a specific period
// Returns limit, isNumberLevel, limitBoth
func getEffectiveLimit(clientSettings *ClientSettings, numberSettings *NumberSettings, msgType string, period string) (int64, bool, bool) {
	var numberLimit, clientLimit int64
	var numberLimitBoth, clientLimitBoth bool

	// Get number-level limit if settings exist
	if numberSettings != nil {
		switch period {
		case "burst":
			if msgType == "mms" {
				numberLimit = numberSettings.MMSBurstLimit
			} else {
				numberLimit = numberSettings.SMSBurstLimit
			}
		case "daily":
			if msgType == "mms" {
				numberLimit = numberSettings.MMSDailyLimit
			} else {
				numberLimit = numberSettings.SMSDailyLimit
			}
		case "monthly":
			if msgType == "mms" {
				numberLimit = numberSettings.MMSMonthlyLimit
			} else {
				numberLimit = numberSettings.SMSMonthlyLimit
			}
		}
		numberLimitBoth = numberSettings.LimitBoth
	}

	// Get client-level limit if settings exist
	if clientSettings != nil {
		switch period {
		case "burst":
			if msgType == "mms" {
				clientLimit = clientSettings.MMSBurstLimit
			} else {
				clientLimit = clientSettings.SMSBurstLimit
			}
		case "daily":
			if msgType == "mms" {
				clientLimit = clientSettings.MMSDailyLimit
			} else {
				clientLimit = clientSettings.SMSDailyLimit
			}
		case "monthly":
			if msgType == "mms" {
				clientLimit = clientSettings.MMSMonthlyLimit
			} else {
				clientLimit = clientSettings.SMSMonthlyLimit
			}
		}
		clientLimitBoth = clientSettings.LimitBoth
	}

	// Number limit takes priority if set (> 0)
	if numberLimit > 0 {
		return numberLimit, true, numberLimitBoth
	}

	// Fall back to client limit
	return clientLimit, false, clientLimitBoth
}

// shouldEnforceLimit checks if the limit should be enforced for this direction
func shouldEnforceLimit(direction string, limitBoth bool) bool {
	if direction == "outbound" {
		return true // Always enforce outbound
	}
	// Inbound only enforced if limitBoth is true
	return limitBoth
}

// GetPeriodStart returns the start time for a limit period in the client's timezone.
// period: "burst", "daily", "monthly"
func GetPeriodStart(client *Client, period string) time.Time {
	now := time.Now()

	// Load client's timezone, default to UTC
	loc := time.UTC
	if client != nil && client.Timezone != "" {
		parsedLoc, err := time.LoadLocation(client.Timezone)
		if err == nil {
			loc = parsedLoc
		}
	}

	// Convert current time to client's timezone
	localNow := now.In(loc)

	switch period {
	case "burst":
		// Last 1 minute
		return now.Add(-time.Minute)
	case "daily":
		// Midnight in client's timezone, converted back to UTC
		localMidnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
		return localMidnight.UTC()
	case "monthly":
		// First day of month at midnight in client's timezone
		localFirstOfMonth := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
		return localFirstOfMonth.UTC()
	default:
		return now.Add(-24 * time.Hour) // Default to daily
	}
}

// CheckMessageLimits performs comprehensive limit checking for a message.
// Returns nil if allowed, or a LimitCheckResult with details if blocked.
func (gateway *Gateway) CheckMessageLimits(client *Client, fromNumber string, msgType string, direction string) *LimitCheckResult {
	if client == nil || client.Settings == nil {
		return nil // No limits configured
	}

	// Find the number's settings
	var numberSettings *NumberSettings
	var numberRef *ClientNumber
	for i := range client.Numbers {
		if strings.Contains(fromNumber, client.Numbers[i].Number) ||
			strings.TrimPrefix(client.Numbers[i].Number, "+") == strings.TrimPrefix(fromNumber, "+") {
			numberRef = &client.Numbers[i]
			numberSettings = client.Numbers[i].Settings
			break
		}
	}

	// Check each period: burst, daily, monthly
	periods := []string{"burst", "daily", "monthly"}
	for _, period := range periods {
		// Get effective limit for this period and message type
		limit, isNumberLevel, limitBoth := getEffectiveLimit(client.Settings, numberSettings, msgType, period)

		// Skip if no limit set
		if limit <= 0 {
			continue
		}

		// Check if we should enforce based on direction
		if !shouldEnforceLimit(direction, limitBoth) {
			continue
		}

		// Get period start time
		periodStart := GetPeriodStart(client, period)

		// Get usage count
		var used int64
		var err error
		if isNumberLevel && numberRef != nil {
			// Number-level: count usage for this specific number
			used, err = gateway.GetUsageCountByType(client.ID, fromNumber, msgType, periodStart)
		} else {
			// Client-level: count total usage for client
			used, err = gateway.GetUsageCountByType(client.ID, "", msgType, periodStart)
		}

		if err != nil {
			continue // Skip on error
		}

		// Check if limit exceeded
		if used >= limit {
			levelStr := "client"
			if isNumberLevel {
				levelStr = "number"
			}
			return &LimitCheckResult{
				Allowed:      false,
				LimitType:    fmt.Sprintf("%s_%s_%s", period, msgType, levelStr),
				CurrentUsage: used,
				Limit:        limit,
				Period:       period,
				Number:       fromNumber,
				Message:      fmt.Sprintf("%s %s limit exceeded (%d/%d)", strings.ToUpper(msgType), period, used, limit),
			}
		}
	}

	return nil // All checks passed
}

// loadClients loads clients from the database, decrypts passwords, and populates the in-memory map.
func (gateway *Gateway) loadClients() error {
	var clients []Client
	// Preload Numbers, Settings, and NumberSettings
	if err := gateway.DB.Preload("Numbers").Preload("Numbers.Settings").Preload("Settings").Find(&clients).Error; err != nil {
		return err
	}

	clientMap := make(map[string]*Client)
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	for _, client := range clients {
		// Decrypt password only (username is stored in plaintext)
		decryptedPassword, err := DecryptAES256(client.Password, gateway.EncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt password for client %s: %w", client.Name, err)
		}

		// Update client struct with decrypted password
		client.Password = decryptedPassword

		c := client // create a copy to avoid referencing the loop variable
		clientMap[client.Username] = &c
	}

	gateway.Clients = clientMap
	return nil
}

func (gateway *Gateway) loadNumbers() error {
	var numbers []ClientNumber
	if err := gateway.DB.Find(&numbers).Error; err != nil {
		return err
	}

	numberMap := make(map[string]*ClientNumber)
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	for _, number := range numbers {
		n := number // create a copy to avoid referencing the loop variable
		numberMap[number.Number] = &n
	}

	gateway.Numbers = numberMap
	return nil
}

// addClient encrypts the client's password and stores the client in the database and in-memory map.
func (gateway *Gateway) addClient(client *Client) error {
	// Store original password for in-memory map
	plaintextPassword := client.Password

	// Encrypt password only (username stored in plaintext)
	encryptedPassword, err := EncryptAES256(client.Password, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	client.Password = encryptedPassword

	// Store in the database
	if err := gateway.DB.Create(client).Error; err != nil {
		return err
	}

	// Restore plaintext password for in-memory map
	client.Password = plaintextPassword

	gateway.mu.Lock()
	gateway.Clients[client.Username] = client
	gateway.mu.Unlock()

	return nil
}

// updateClientPassword updates a client's password by ID (password is write-only, never returned)
func (gateway *Gateway) updateClientPassword(clientID uint, newPassword string) error {
	// Find client in memory
	client := gateway.getClientByID(clientID)
	if client == nil {
		return fmt.Errorf("client not found in memory")
	}

	// Encrypt the new password
	encryptedPassword, err := EncryptAES256(newPassword, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt new password: %w", err)
	}

	// Update in database
	if err := gateway.DB.Model(&Client{}).Where("id = ?", clientID).Update("password", encryptedPassword).Error; err != nil {
		return fmt.Errorf("failed to update password in database: %w", err)
	}

	// Update in memory
	gateway.mu.Lock()
	client.Password = newPassword // Store decrypted in memory
	gateway.mu.Unlock()

	return nil
}

// addNumber adds a new number to a client by ID.
func (gateway *Gateway) addNumber(clientID uint, number *ClientNumber) error {
	// Normalize number to E.164 without + prefix
	// Strip leading + if present, ensure it starts with country code
	normalizedNumber := strings.TrimPrefix(number.Number, "+")
	// Remove any non-digit characters
	normalizedNumber = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, normalizedNumber)
	number.Number = normalizedNumber

	// Check if the client exists
	client := gateway.getClientByID(clientID)
	if client == nil {
		return fmt.Errorf("client with ID %d does not exist", clientID)
	}

	// Validate if the carrier exists
	gateway.mu.RLock()
	_, carrierExists := gateway.Carriers[number.Carrier]
	gateway.mu.RUnlock()
	if !carrierExists {
		return fmt.Errorf("carrier %s does not exist", number.Carrier)
	}

	// Check if the number already exists
	gateway.mu.RLock()
	_, numberExists := gateway.Numbers[number.Number]
	gateway.mu.RUnlock()
	if numberExists {
		return fmt.Errorf("number %s already exists", number.Number)
	}

	number.ClientID = client.ID

	// Create the number in the database
	if err := gateway.DB.Create(number).Error; err != nil {
		return fmt.Errorf("failed to add number to database: %w", err)
	}

	// Add the number to the in-memory map
	gateway.mu.Lock()
	gateway.Numbers[number.Number] = number
	gateway.mu.Unlock()

	// Log the addition
	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"Client.AddNumber",
		fmt.Sprintf("Added number %s to client %s", number.Number, client.Username),
		logrus.InfoLevel,
		map[string]interface{}{
			"client_id": client.ID,
			"number":    number.Number,
			"carrier":   number.Carrier,
		},
	))

	return nil
}

// reloadClientsAndNumbers reloads clients and numbers from the database.
func (gateway *Gateway) reloadClientsAndNumbers() error {
	if err := gateway.loadClients(); err != nil {
		return err
	}
	if err := gateway.loadNumbers(); err != nil {
		return err
	}
	return nil
}

func (gateway *Gateway) authClient(username string, password string) (bool, error) {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	client, exists := gateway.Clients[username]
	if !exists {
		return false, nil
	}
	return client.Password == password, nil
}

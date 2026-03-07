package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// TenantAPIKey represents a scoped API key bound to a client.
// Keys are hashed (SHA-256) at rest — the raw key is only returned once on creation.
type TenantAPIKey struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	ClientID   uint       `gorm:"index;not null" json:"client_id"`
	Name       string     `json:"name"`                 // Human label, e.g. "CSV Import App"
	KeyHash    string     `gorm:"uniqueIndex" json:"-"` // SHA-256 hash of the raw key
	KeyPrefix  string     `json:"key_prefix"`           // First 8 chars for identification (e.g. "gw_live_")
	Scopes     string     `json:"scopes"`               // Comma-separated: "send", "batch", "usage"
	RateLimit  int        `json:"rate_limit"`           // Requests per minute (0 = use client limit)
	Active     bool       `gorm:"default:true" json:"active"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`

	// Number-level scoping
	AllowedNumbers []APIKeyNumber `gorm:"foreignKey:APIKeyID" json:"allowed_numbers"`
}

// APIKeyNumber is a join table scoping a key to specific ClientNumbers.
// If a key has zero entries, it can use ALL numbers on the parent client.
type APIKeyNumber struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	APIKeyID uint   `gorm:"index;not null" json:"api_key_id"`
	NumberID uint   `gorm:"index;not null" json:"number_id"` // FK to ClientNumber
	Number   string `json:"number"`                          // Denormalized E.164 for fast lookup
}

const (
	apiKeyPrefix    = "gw_live_"
	apiKeyRandomLen = 32 // bytes of randomness
)

// GenerateAPIKey creates a new raw API key string.
// Format: gw_live_<64 hex chars> (total ~72 chars)
func GenerateAPIKey() (string, error) {
	b := make([]byte, apiKeyRandomLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return apiKeyPrefix + hex.EncodeToString(b), nil
}

// HashAPIKey returns the SHA-256 hex digest of a raw API key.
func HashAPIKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(h[:])
}

// HasScope checks if the key has a specific scope.
func (k *TenantAPIKey) HasScope(scope string) bool {
	for _, s := range strings.Split(k.Scopes, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}

// IsExpired checks if the key has passed its expiry time.
func (k *TenantAPIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// IsNumberAllowed checks if a number (E.164 digits only) is allowed for this key.
// If the key has no AllowedNumbers, all client numbers are permitted.
func (k *TenantAPIKey) IsNumberAllowed(number string) bool {
	if len(k.AllowedNumbers) == 0 {
		return true // no restrictions — all client numbers allowed
	}
	cleanNumber := strings.TrimPrefix(number, "+")
	for _, an := range k.AllowedNumbers {
		if an.Number == cleanNumber || an.Number == number {
			return true
		}
	}
	return false
}

// TouchLastUsed updates the LastUsedAt timestamp (fire-and-forget to DB).
func (k *TenantAPIKey) TouchLastUsed(db *gorm.DB) {
	now := time.Now()
	k.LastUsedAt = &now
	go func() {
		db.Model(&TenantAPIKey{}).Where("id = ?", k.ID).Update("last_used_at", now)
	}()
}

// --- Gateway methods for API key management ---

// loadAPIKeys loads all active API keys (with AllowedNumbers) into memory.
func (gateway *Gateway) loadAPIKeys() error {
	var keys []TenantAPIKey
	if err := gateway.DB.Where("active = ?", true).Preload("AllowedNumbers").Find(&keys).Error; err != nil {
		return fmt.Errorf("failed to load API keys: %w", err)
	}

	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.APIKeys = make(map[string]*TenantAPIKey)
	for i := range keys {
		gateway.APIKeys[keys[i].KeyHash] = &keys[i]
	}

	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"System.APIKeys",
		"Loaded API keys",
		logrus.InfoLevel,
		map[string]interface{}{
			"count": len(keys),
		},
	))

	return nil
}

// lookupAPIKey finds an API key by its raw token value.
// Returns nil if not found, inactive, or expired.
func (gateway *Gateway) lookupAPIKey(rawKey string) *TenantAPIKey {
	hash := HashAPIKey(rawKey)

	gateway.mu.RLock()
	key, exists := gateway.APIKeys[hash]
	gateway.mu.RUnlock()

	if !exists || !key.Active || key.IsExpired() {
		return nil
	}
	return key
}

// createAPIKey generates a new API key for a client, stores it, and returns the raw key.
func (gateway *Gateway) createAPIKey(clientID uint, name string, scopes string, rateLimit int, expiresAt *time.Time, allowedNumberIDs []uint) (string, *TenantAPIKey, error) {
	// Verify client exists
	client := gateway.getClientByID(clientID)
	if client == nil {
		return "", nil, fmt.Errorf("client not found")
	}

	// Generate raw key
	rawKey, err := GenerateAPIKey()
	if err != nil {
		return "", nil, err
	}

	keyHash := HashAPIKey(rawKey)
	keyPrefix := rawKey[:len(apiKeyPrefix)+8] // "gw_live_" + first 8 hex chars

	apiKey := &TenantAPIKey{
		ClientID:  clientID,
		Name:      name,
		KeyHash:   keyHash,
		KeyPrefix: keyPrefix,
		Scopes:    scopes,
		RateLimit: rateLimit,
		Active:    true,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}

	// Resolve allowed numbers
	if len(allowedNumberIDs) > 0 {
		for _, numID := range allowedNumberIDs {
			// Find the number in the client's numbers
			found := false
			for _, cn := range client.Numbers {
				if cn.ID == numID {
					apiKey.AllowedNumbers = append(apiKey.AllowedNumbers, APIKeyNumber{
						NumberID: numID,
						Number:   cn.Number,
					})
					found = true
					break
				}
			}
			if !found {
				return "", nil, fmt.Errorf("number ID %d not found on client %d", numID, clientID)
			}
		}
	}

	// Save to DB
	if err := gateway.DB.Create(apiKey).Error; err != nil {
		return "", nil, fmt.Errorf("failed to create API key: %w", err)
	}

	// Add to in-memory map
	gateway.mu.Lock()
	gateway.APIKeys[keyHash] = apiKey
	gateway.mu.Unlock()

	return rawKey, apiKey, nil
}

// revokeAPIKey deactivates an API key.
func (gateway *Gateway) revokeAPIKey(keyID uint, clientID uint) error {
	result := gateway.DB.Model(&TenantAPIKey{}).
		Where("id = ? AND client_id = ?", keyID, clientID).
		Update("active", false)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}

	// Remove from in-memory map
	gateway.mu.Lock()
	for hash, key := range gateway.APIKeys {
		if key.ID == keyID {
			delete(gateway.APIKeys, hash)
			break
		}
	}
	gateway.mu.Unlock()

	return nil
}

// listAPIKeys returns all API keys for a client (never including the hash).
func (gateway *Gateway) listAPIKeys(clientID uint) ([]TenantAPIKey, error) {
	var keys []TenantAPIKey
	if err := gateway.DB.Where("client_id = ?", clientID).Preload("AllowedNumbers").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

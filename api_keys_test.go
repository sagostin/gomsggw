package main

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAPIKey_Format(t *testing.T) {
	key, err := GenerateAPIKey()
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(key, "gw_live_"),
		"key should start with the gw_live_ prefix")

	// "gw_live_" (8 chars) + 64 hex chars = 72 chars total
	assert.Equal(t, 72, len(key), "key should be 8 + 64 = 72 characters")

	// The part after the prefix must be valid hex
	hexPart := key[len("gw_live_"):]
	_, err = hex.DecodeString(hexPart)
	assert.NoError(t, err, "suffix must be valid hex")
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	const n = 100
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		k, err := GenerateAPIKey()
		require.NoError(t, err)
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate key generated: %s", k)
		}
		seen[k] = struct{}{}
	}
}

func TestHashAPIKey_Deterministic(t *testing.T) {
	raw := "gw_live_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	h1 := HashAPIKey(raw)
	h2 := HashAPIKey(raw)
	assert.Equal(t, h1, h2)
	assert.Equal(t, 64, len(h1), "SHA-256 hex digest is 64 chars")
}

func TestHashAPIKey_DifferentInputs(t *testing.T) {
	a := HashAPIKey("gw_live_aaaa")
	b := HashAPIKey("gw_live_bbbb")
	assert.NotEqual(t, a, b)
}

func TestTenantAPIKey_HasScope(t *testing.T) {
	k := &TenantAPIKey{Scopes: "send,batch,usage"}

	assert.True(t, k.HasScope("send"))
	assert.True(t, k.HasScope("batch"))
	assert.True(t, k.HasScope("usage"))
	assert.False(t, k.HasScope("admin"))
	assert.False(t, k.HasScope(""))
}

func TestTenantAPIKey_HasScope_Whitespace(t *testing.T) {
	// Should tolerate stray whitespace between scopes.
	k := &TenantAPIKey{Scopes: " send , batch ,usage"}
	assert.True(t, k.HasScope("send"))
	assert.True(t, k.HasScope("batch"))
	assert.True(t, k.HasScope("usage"))
}

func TestTenantAPIKey_IsExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	assert.False(t, (&TenantAPIKey{ExpiresAt: nil}).IsExpired(), "nil = never expires")

	k := &TenantAPIKey{ExpiresAt: &past}
	assert.True(t, k.IsExpired(), "past expiry should be expired")

	k = &TenantAPIKey{ExpiresAt: &future}
	assert.False(t, k.IsExpired(), "future expiry should NOT be expired")
}

func TestTenantAPIKey_IsNumberAllowed_NoRestrictions(t *testing.T) {
	k := &TenantAPIKey{AllowedNumbers: nil}
	assert.True(t, k.IsNumberAllowed("12505551234"))
	assert.True(t, k.IsNumberAllowed("+12505551234"))
}

func TestTenantAPIKey_IsNumberAllowed_Restricted(t *testing.T) {
	k := &TenantAPIKey{
		AllowedNumbers: []APIKeyNumber{
			{Number: "12505551234"},
			{Number: "12505555678"},
		},
	}

	// Allowed (with and without +)
	assert.True(t, k.IsNumberAllowed("12505551234"))
	assert.True(t, k.IsNumberAllowed("+12505551234"))

	// Not in the list
	assert.False(t, k.IsNumberAllowed("12505559999"))
	assert.False(t, k.IsNumberAllowed("+12505559999"))
}

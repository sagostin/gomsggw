package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPSK = "abcdefghijklmnopqrstuvwxyz123456" // 32 bytes

func TestEncryptDecryptAES256_RoundTrip(t *testing.T) {
	plaintexts := []string{
		"",
		"a",
		"super-secret-password",
		strings.Repeat("x", 1000), // larger than AES block
		"unicode: 你好世界 🌍",
	}

	for _, pt := range plaintexts {
		t.Run(pt, func(t *testing.T) {
			ct, err := EncryptAES256(pt, testPSK)
			require.NoError(t, err)
			require.NotEmpty(t, ct)

			decoded, err := DecryptAES256(ct, testPSK)
			require.NoError(t, err)
			assert.Equal(t, pt, decoded)
		})
	}
}

func TestEncryptAES256_DifferentIVs(t *testing.T) {
	// Same plaintext, same key → different ciphertexts (IV is random).
	ct1, err := EncryptAES256("identical-input", testPSK)
	require.NoError(t, err)
	ct2, err := EncryptAES256("identical-input", testPSK)
	require.NoError(t, err)
	assert.NotEqual(t, ct1, ct2, "ciphertexts should differ because of random IV")
}

func TestEncryptAES256_AcceptsShortAndLongKeys(t *testing.T) {
	// The implementation pads keys < 32 bytes with zero bytes and truncates
	// keys > 32 bytes to the first 32. Two keys that reduce to the same 32
	// bytes are interchangeable; two that don't, are not.
	shortKey := "short"
	shortKeyPadded := "short" + strings.Repeat("\x00", 27)
	longKeyDifferent := strings.Repeat("k", 32)

	ct, err := EncryptAES256("hello", shortKey)
	require.NoError(t, err)

	// Same effective 32 bytes → round-trips correctly.
	decoded, err := DecryptAES256(ct, shortKeyPadded)
	require.NoError(t, err)
	assert.Equal(t, "hello", decoded)

	// Different effective 32 bytes → does NOT round-trip to the plaintext.
	decoded, err = DecryptAES256(ct, longKeyDifferent)
	require.NoError(t, err)
	assert.NotEqual(t, "hello", decoded)
}

func TestDecryptAES256_WrongKey(t *testing.T) {
	ct, err := EncryptAES256("top-secret", testPSK)
	require.NoError(t, err)

	// CFB is a stream cipher; wrong key still produces output, but it will be garbage.
	decoded, err := DecryptAES256(ct, "different-32-byte-key-aaaaaaaaa")
	require.NoError(t, err)
	assert.NotEqual(t, "top-secret", decoded)
}

func TestDecryptAES256_InvalidBase64(t *testing.T) {
	_, err := DecryptAES256("!!!not-base64!!!", testPSK)
	assert.Error(t, err)
}

func TestDecryptAES256_TooShort(t *testing.T) {
	// "AAAA" is 4 bytes of base64 → 3 raw bytes, which is shorter than the 16-byte IV.
	_, err := DecryptAES256("AAAA", testPSK)
	assert.Error(t, err)
}

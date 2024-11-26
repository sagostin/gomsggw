// encrypt.go
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// EncryptPassword encrypts a plaintext password using AES-256 with the provided PSK.
func EncryptPassword(password, psk string) (string, error) {
	// Convert PSK to 32 bytes (AES-256 key size)
	key := []byte(psk)
	if len(key) > 32 {
		key = key[:32]
	} else if len(key) < 32 {
		padded := make([]byte, 32)
		copy(padded, key)
		key = padded
	}

	// Create AES block cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Generate a random IV
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", fmt.Errorf("failed to generate IV: %w", err)
	}

	// Encrypt the password
	ciphertext := make([]byte, len(password))
	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext, []byte(password))

	// Prepend IV to ciphertext
	combined := append(iv, ciphertext...)

	// Encode the result in base64
	return base64.StdEncoding.EncodeToString(combined), nil
}

// DecryptPassword decrypts an AES-256 encrypted password using the provided PSK.
func DecryptPassword(encryptedBase64, psk string) (string, error) {
	// Convert PSK to 32 bytes (AES-256 key size)
	key := []byte(psk)
	if len(key) > 32 {
		key = key[:32]
	} else if len(key) < 32 {
		padded := make([]byte, 32)
		copy(padded, key)
		key = padded
	}

	// Decode the base64 encoded ciphertext
	combined, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	if len(combined) < aes.BlockSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Extract the IV and ciphertext
	iv := combined[:aes.BlockSize]
	ciphertext := combined[aes.BlockSize:]

	// Create AES block cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Decrypt the data
	plaintext := make([]byte, len(ciphertext))
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(plaintext, ciphertext)

	return string(plaintext), nil
}

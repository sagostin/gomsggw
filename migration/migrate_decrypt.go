// ==============================================================================
// GOMSGGW Migration Tool: Decrypt Usernames
// ==============================================================================
// This tool migrates data from the old schema (encrypted usernames) to the new
// schema (plaintext usernames).
//
// Usage:
//   go run migrate_decrypt.go -key=YOUR_ENCRYPTION_KEY [-dry-run] [-dsn="..."]
//
// The tool will:
// 1. Read all clients from the database
// 2. Decrypt usernames that appear to be encrypted
// 3. Update the database with plaintext usernames
// 4. Report what was changed
// ==============================================================================

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

func main() {
	// Parse command line flags
	encryptionKey := flag.String("key", "", "32-byte encryption key (required)")
	dryRun := flag.Bool("dry-run", false, "Show what would be changed without modifying database")
	dsn := flag.String("dsn", "", "PostgreSQL DSN (defaults to env vars)")
	flag.Parse()

	if *encryptionKey == "" {
		// Try environment variable
		*encryptionKey = os.Getenv("ENCRYPTION_KEY")
	}

	if *encryptionKey == "" {
		log.Fatal("Error: -key flag or ENCRYPTION_KEY env var is required")
	}

	if len(*encryptionKey) != 32 {
		log.Fatalf("Error: Encryption key must be exactly 32 characters, got %d", len(*encryptionKey))
	}

	// Build DSN from environment if not provided
	if *dsn == "" {
		*dsn = buildDSN()
	}

	log.Printf("Connecting to database...")

	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Printf("Connected successfully!")

	// Migrate clients
	if err := migrateClients(db, *encryptionKey, *dryRun); err != nil {
		log.Fatalf("Failed to migrate clients: %v", err)
	}

	log.Println("Migration completed successfully!")
}

func buildDSN() string {
	host := getEnv("POSTGRES_HOST", "localhost")
	port := getEnv("POSTGRES_PORT", "5432")
	user := getEnv("POSTGRES_USER", "smsgw")
	password := getEnv("POSTGRES_PASSWORD", "")
	dbname := getEnv("POSTGRES_DB", "smsgw")
	sslmode := getEnv("POSTGRES_SSLMODE", "disable")

	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func migrateClients(db *sql.DB, encryptionKey string, dryRun bool) error {
	log.Println("=== Migrating Clients ===")

	rows, err := db.Query("SELECT id, username, name FROM clients ORDER BY id")
	if err != nil {
		return fmt.Errorf("failed to query clients: %w", err)
	}
	defer rows.Close()

	var updates []struct {
		ID          int64
		OldUsername string
		NewUsername string
		Name        string
	}

	for rows.Next() {
		var id int64
		var username, name sql.NullString

		if err := rows.Scan(&id, &username, &name); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		if !username.Valid || username.String == "" {
			continue
		}

		// Try to decrypt the username
		decrypted, err := DecryptAES256(username.String, encryptionKey)
		if err != nil {
			// If decryption fails, it's probably already plaintext
			log.Printf("  Client %d (%s): Already plaintext or different encryption", id, username.String)
			continue
		}

		// Check if decryption actually changed anything
		if decrypted == username.String {
			log.Printf("  Client %d (%s): Already plaintext", id, username.String)
			continue
		}

		updates = append(updates, struct {
			ID          int64
			OldUsername string
			NewUsername string
			Name        string
		}{
			ID:          id,
			OldUsername: username.String,
			NewUsername: decrypted,
			Name:        name.String,
		})
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	if len(updates) == 0 {
		log.Println("No clients need migration (all usernames already plaintext)")
		return nil
	}

	log.Printf("Found %d clients to migrate:", len(updates))
	for _, u := range updates {
		log.Printf("  ID %d: '%s' -> '%s' (%s)", u.ID, u.OldUsername[:min(20, len(u.OldUsername))]+"...", u.NewUsername, u.Name)
	}

	if dryRun {
		log.Println("\n[DRY RUN] No changes made")
		return nil
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Update each client
	stmt, err := tx.Prepare("UPDATE clients SET username = $1 WHERE id = $2")
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, u := range updates {
		if _, err := stmt.Exec(u.NewUsername, u.ID); err != nil {
			return fmt.Errorf("failed to update client %d: %w", u.ID, err)
		}
		log.Printf("  Updated client %d: %s", u.ID, u.NewUsername)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Successfully migrated %d clients", len(updates))
	return nil
}

// DecryptAES256 decrypts data encrypted with AES-256-CBC.
func DecryptAES256(encrypted string, key string) (string, error) {
	// The encrypted data is base64 encoded
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	// AES block size is 16 bytes
	if len(data) < aes.BlockSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// IV is the first 16 bytes
	iv := data[:aes.BlockSize]
	ciphertext := data[aes.BlockSize:]

	if len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext is not a multiple of block size")
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding
	plaintext, err = pkcs7Unpad(plaintext)
	if err != nil {
		return "", fmt.Errorf("failed to unpad: %w", err)
	}

	return string(plaintext), nil
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[len(data)-1])
	if padding > aes.BlockSize || padding == 0 {
		return nil, fmt.Errorf("invalid padding size")
	}
	for i := 0; i < padding; i++ {
		if data[len(data)-1-i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding bytes")
		}
	}
	return data[:len(data)-padding], nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

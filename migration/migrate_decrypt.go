// ==============================================================================
// GOMSGGW Migration Tool: Re-key Encrypted Data
// ==============================================================================
// This tool migrates data from the old encryption key (which may have been blank)
// to a new encryption key. Usernames are stored as plaintext, passwords are
// re-encrypted with the new key.
//
// Environment Variables:
//   OLD_ENCRYPTION_KEY - The key originally used to encrypt the data (may be blank)
//   ENCRYPTION_KEY     - The new key to use for password re-encryption
//
// Usage:
//   go run migrate_decrypt.go [-dry-run] [-dsn="..."]
//
// The tool will:
// 1. Read all clients from the database
// 2. Decrypt usernames AND passwords using OLD_ENCRYPTION_KEY
// 3. Store usernames as plaintext
// 4. Re-encrypt passwords using ENCRYPTION_KEY
// 5. Report what was changed
// ==============================================================================

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {

	// Parse command line flags
	dryRun := flag.Bool("dry-run", false, "Show what would be changed without modifying database")
	flag.Parse()

	err := godotenv.Load()
	if err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
	}

	// Get both encryption keys
	oldEncryptionKey := os.Getenv("OLD_ENCRYPTION_KEY")
	newEncryptionKey := os.Getenv("ENCRYPTION_KEY")

	if newEncryptionKey == "" {
		log.Fatalf("ENCRYPTION_KEY must be set for re-encryption")
	}

	log.Printf("OLD_ENCRYPTION_KEY: '%s' (length: %d)", maskKey(oldEncryptionKey), len(oldEncryptionKey))
	log.Printf("ENCRYPTION_KEY: '%s' (length: %d)", maskKey(newEncryptionKey), len(newEncryptionKey))

	if oldEncryptionKey == newEncryptionKey {
		log.Println("WARNING: Old and new encryption keys are the same. Only usernames will be converted to plaintext.")
	}

	dns := buildDSN()

	log.Printf("Connecting to database...")

	db, err := sql.Open("postgres", dns)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Printf("Connected successfully!")

	// Migrate clients
	if err := migrateClients(db, oldEncryptionKey, newEncryptionKey, *dryRun); err != nil {
		log.Fatalf("Failed to migrate clients: %v", err)
	}

	log.Println("Migration completed successfully!")
}

// maskKey returns a masked version of the key for logging
func maskKey(key string) string {
	if key == "" {
		return "(empty)"
	}
	if len(key) <= 4 {
		return "****"
	}
	return key[:2] + "****" + key[len(key)-2:]
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

type clientUpdate struct {
	ID             int64
	Name           string
	OldUsername    string
	NewUsername    string
	OldPassword    string
	NewPassword    string
	UsernameChange bool
	PasswordChange bool
}

func migrateClients(db *sql.DB, oldKey, newKey string, dryRun bool) error {
	log.Println("=== Migrating Clients ===")

	rows, err := db.Query("SELECT id, username, password, name FROM clients ORDER BY id")
	if err != nil {
		return fmt.Errorf("failed to query clients: %w", err)
	}
	defer rows.Close()

	var updates []clientUpdate

	for rows.Next() {
		var id int64
		var username, password, name sql.NullString

		if err := rows.Scan(&id, &username, &password, &name); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		update := clientUpdate{
			ID:          id,
			Name:        name.String,
			OldUsername: username.String,
			OldPassword: password.String,
		}

		// Try to decrypt the username with old key
		if username.Valid && username.String != "" {
			decryptedUsername, err := DecryptAES256(username.String, oldKey)
			if err != nil {
				// If decryption fails, it's probably already plaintext
				log.Printf("  Client %d: Username appears to already be plaintext or uses different encryption", id)
				update.NewUsername = username.String
			} else if decryptedUsername == username.String {
				log.Printf("  Client %d: Username already plaintext", id)
				update.NewUsername = username.String
			} else {
				update.NewUsername = decryptedUsername
				update.UsernameChange = true
				log.Printf("  Client %d: Username will be decrypted: '%s...' -> '%s'",
					id, truncate(username.String, 20), decryptedUsername)
			}
		}

		// Try to decrypt the password with old key, then re-encrypt with new key
		if password.Valid && password.String != "" {
			decryptedPassword, err := DecryptAES256(password.String, oldKey)
			if err != nil {
				// If decryption fails, password might already be in a different format
				log.Printf("  Client %d: Password decryption failed (may use different encryption): %v", id, err)
				update.NewPassword = password.String
			} else {
				// Re-encrypt the password with the new key
				reencryptedPassword, err := EncryptAES256(decryptedPassword, newKey)
				if err != nil {
					return fmt.Errorf("failed to re-encrypt password for client %d: %w", id, err)
				}

				// Only mark as changed if the keys are different
				if oldKey != newKey {
					update.NewPassword = reencryptedPassword
					update.PasswordChange = true
					log.Printf("  Client %d: Password will be re-encrypted with new key", id)
				} else {
					update.NewPassword = password.String
					log.Printf("  Client %d: Password unchanged (same key)", id)
				}
			}
		}

		// Only add to updates if something changed
		if update.UsernameChange || update.PasswordChange {
			updates = append(updates, update)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	if len(updates) == 0 {
		log.Println("No clients need migration")
		return nil
	}

	log.Printf("\n=== Summary: %d clients to migrate ===", len(updates))
	for _, u := range updates {
		changes := []string{}
		if u.UsernameChange {
			changes = append(changes, fmt.Sprintf("username: '%s...' -> '%s'", truncate(u.OldUsername, 15), u.NewUsername))
		}
		if u.PasswordChange {
			changes = append(changes, "password: re-encrypted")
		}
		log.Printf("  ID %d (%s): %v", u.ID, u.Name, changes)
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
	stmt, err := tx.Prepare("UPDATE clients SET username = $1, password = $2 WHERE id = $3")
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, u := range updates {
		if _, err := stmt.Exec(u.NewUsername, u.NewPassword, u.ID); err != nil {
			return fmt.Errorf("failed to update client %d: %w", u.ID, err)
		}
		log.Printf("  Updated client %d: %s", u.ID, u.Name)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Successfully migrated %d clients", len(updates))
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// EncryptAES256 encrypts a plaintext password using AES-256 with the provided PSK.
func EncryptAES256(password, psk string) (string, error) {
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

// DecryptAES256 decrypts an AES-256 encrypted password using the provided PSK.
func DecryptAES256(encryptedBase64, psk string) (string, error) {
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

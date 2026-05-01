package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite" // SQLite driver for modernc.org
)

// CommandQueue persists PMS events to SQLite when cloud is unreachable,
// and flushes them in order when connectivity is restored.
type CommandQueue struct {
	db *sql.DB

	// Config
	maxSizeBytes int64 // max queue size in bytes (default 100MB)
	maxItems     int   // max number of items (safety cap)

	// Metrics
	sizeBytes  int64
	itemCount  int64
	flushCount int64

	mu sync.RWMutex
}

// CommandQueueItem is the persisted representation of a queued event.
type CommandQueueItem struct {
	ID            string    `json:"id"`             // unique event ID (for dedup)
	EventType     string    `json:"event_type"`     // e.g. "sms", "mms"
	ToNumber      string    `json:"to_number"`
	FromNumber    string    `json:"from_number"`
	Payload       string    `json:"payload"`        // JSON-encoded MsgQueueItem
	CreatedAt     time.Time `json:"created_at"`     // when queued
	NextRetryAt   time.Time `json:"next_retry_at"` // for exponential backoff
	RetryCount    int       `json:"retry_count"`
	Status        string    `json:"status"`         // pending, sending, acknowledged, failed
	AckedAt       *time.Time `json:"acked_at,omitempty"`
	SizeBytes     int64     `json:"size_bytes"`    // payload size for capacity tracking
}

// CloudStatus represents whether the cloud connection is available.
type CloudStatus int

const (
	CloudStatusUnknown CloudStatus = iota
	CloudStatusOnline
	CloudStatusOffline
)

// String returns a human-readable representation of CloudStatus.
func (s CloudStatus) String() string {
	switch s {
	case CloudStatusOnline:
		return "online"
	case CloudStatusOffline:
		return "offline"
	default:
		return "unknown"
	}
}

// NewCommandQueue initializes the SQLite-backed command queue.
// dbPath is the path to the SQLite database file.
func NewCommandQueue(dbPath string) (*CommandQueue, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating queue db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}

	// Enable foreign keys and WAL mode for durability
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	q := &CommandQueue{
		db:            db,
		maxSizeBytes:  100 * 1024 * 1024, // 100MB default
		maxItems:      100000,            // safety cap
		sizeBytes:     0,
		itemCount:     0,
		flushCount:    0,
	}

	if err := q.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}

	// Load current metrics
	q.recalculateMetrics()

	return q, nil
}

// migrate creates the command_queue table if it doesn't exist.
func (q *CommandQueue) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS command_queue (
		id            TEXT PRIMARY KEY,
		event_type    TEXT NOT NULL,
		to_number     TEXT NOT NULL,
		from_number   TEXT NOT NULL,
		payload       TEXT NOT NULL,
		created_at    DATETIME NOT NULL,
		next_retry_at DATETIME NOT NULL,
		retry_count   INTEGER NOT NULL DEFAULT 0,
		status        TEXT NOT NULL DEFAULT 'pending',
		acked_at      DATETIME,
		size_bytes    INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_cmd_queue_status ON command_queue(status);
	CREATE INDEX IF NOT EXISTS idx_cmd_queue_next_retry ON command_queue(next_retry_at);
	CREATE INDEX IF NOT EXISTS idx_cmd_queue_created ON command_queue(created_at);
	`
	_, err := q.db.Exec(schema)
	return err
}

// recalculateMetrics recomputes sizeBytes and itemCount from the DB.
func (q *CommandQueue) recalculateMetrics() {
	var sizeBytes int64
	var itemCount int64
	row := q.db.QueryRow("SELECT COALESCE(SUM(size_bytes),0), COUNT(*) FROM command_queue WHERE status = 'pending' OR status = 'sending'")
	if err := row.Scan(&sizeBytes, &itemCount); err != nil {
		return
	}
	q.mu.Lock()
	q.sizeBytes = sizeBytes
	q.itemCount = itemCount
	q.mu.Unlock()
}

// Enqueue adds an event to the queue. It returns the item ID and any error.
// If an item with the same ID already exists (dedup), it returns the existing ID and nil.
func (q *CommandQueue) Enqueue(ctx context.Context, item *MsgQueueItem, eventType string) (string, error) {
	// Generate event ID if not set
	eventID := item.LogID
	if eventID == "" {
		eventID = uuid.New().String()
	}

	payload, err := json.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("marshaling payload: %w", err)
	}

	sizeBytes := int64(len(payload))

	q.mu.Lock()
	defer q.mu.Unlock()

	// Check capacity
	if q.sizeBytes+sizeBytes > q.maxSizeBytes {
		return "", fmt.Errorf("queue capacity exceeded: %d + %d > %d bytes", q.sizeBytes, sizeBytes, q.maxSizeBytes)
	}
	if q.itemCount >= int64(q.maxItems) {
		return "", fmt.Errorf("queue item limit exceeded: %d items", q.itemCount)
	}

	// Check for duplicate
	var existingID string
	err = q.db.QueryRowContext(ctx, "SELECT id FROM command_queue WHERE id = ?", eventID).Scan(&existingID)
	if err == nil {
		// Duplicate found
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("checking dedup: %w", err)
	}

	now := time.Now()
	qi := CommandQueueItem{
		ID:          eventID,
		EventType:   eventType,
		ToNumber:    item.To,
		FromNumber:  item.From,
		Payload:     string(payload),
		CreatedAt:   now,
		NextRetryAt: now,
		RetryCount:  0,
		Status:      "pending",
		SizeBytes:   sizeBytes,
	}

	_, err = q.db.ExecContext(ctx, `
		INSERT INTO command_queue (id, event_type, to_number, from_number, payload, created_at, next_retry_at, retry_count, status, size_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		qi.ID, qi.EventType, qi.ToNumber, qi.FromNumber, qi.Payload,
		qi.CreatedAt, qi.NextRetryAt, qi.RetryCount, qi.Status, qi.SizeBytes)
	if err != nil {
		return "", fmt.Errorf("inserting queue item: %w", err)
	}

	q.sizeBytes += sizeBytes
	q.itemCount++

	return eventID, nil
}

// DequeuePending fetches the next item that is due for retry (oldest-first), respecting capacity and dedup.
func (q *CommandQueue) DequeuePending(ctx context.Context) (*CommandQueueItem, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var item CommandQueueItem
	now := time.Now()
	err := q.db.QueryRowContext(ctx, `
		SELECT id, event_type, to_number, from_number, payload, created_at, next_retry_at, retry_count, status, size_bytes
		FROM command_queue
		WHERE status = 'pending' AND next_retry_at <= ?
		ORDER BY created_at ASC
		LIMIT 1`, now).Scan(
		&item.ID, &item.EventType, &item.ToNumber, &item.FromNumber,
		&item.Payload, &item.CreatedAt, &item.NextRetryAt, &item.RetryCount,
		&item.Status, &item.SizeBytes)
	if err == sql.ErrNoRows {
		return nil, nil // nothing due
	}
	if err != nil {
		return nil, fmt.Errorf("dequeue query: %w", err)
	}

	// Mark as sending to prevent double-send during concurrent flush
	_, err = q.db.ExecContext(ctx, "UPDATE command_queue SET status = 'sending' WHERE id = ? AND status = 'pending'", item.ID)
	if err != nil {
		return nil, fmt.Errorf("marking sending: %w", err)
	}

	return &item, nil
}

// MarkAcknowledged marks an item as successfully sent and removes it from the queue.
func (q *CommandQueue) MarkAcknowledged(ctx context.Context, eventID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Get size before delete
	var sizeBytes int64
	err := q.db.QueryRowContext(ctx, "SELECT size_bytes FROM command_queue WHERE id = ?", eventID).Scan(&sizeBytes)
	if err == sql.ErrNoRows {
		return nil // already gone
	}
	if err != nil {
		return fmt.Errorf("getting size: %w", err)
	}

	_, err = q.db.ExecContext(ctx, "DELETE FROM command_queue WHERE id = ?", eventID)
	if err != nil {
		return fmt.Errorf("deleting item: %w", err)
	}

	q.sizeBytes -= sizeBytes
	q.itemCount--
	q.flushCount++

	return nil
}

// MarkFailed records a failed attempt and schedules a retry with exponential backoff.
func (q *CommandQueue) MarkFailed(ctx context.Context, eventID string, retryCount int) error {
	if retryCount >= 5 {
		// Max retries reached — mark as failed and remove
		q.mu.Lock()
		_, err := q.db.ExecContext(ctx, "UPDATE command_queue SET status = 'failed', retry_count = ? WHERE id = ?", retryCount, eventID)
		q.mu.Unlock()
		if err != nil {
			return fmt.Errorf("marking failed: %w", err)
		}
		// Remove from count
		var sizeBytes int64
		q.db.QueryRowContext(ctx, "SELECT size_bytes FROM command_queue WHERE id = ?", eventID).Scan(&sizeBytes)
		q.mu.Lock()
		q.sizeBytes -= sizeBytes
		q.itemCount--
		q.mu.Unlock()
		return nil
	}

	// Exponential backoff: 10s, 20s, 40s, 80s, 160s
	backoffSecs := 10 * (1 << retryCount)
	nextRetry := time.Now().Add(time.Duration(backoffSecs) * time.Second)

	_, err := q.db.ExecContext(ctx,
		"UPDATE command_queue SET status = 'pending', retry_count = ?, next_retry_at = ? WHERE id = ?",
		retryCount, nextRetry, eventID)
	if err != nil {
		return fmt.Errorf("scheduling retry: %w", err)
	}
	return nil
}

// FlushAll returns all pending and sending items in creation order for a full flush on reconnect.
func (q *CommandQueue) FlushAll(ctx context.Context) ([]CommandQueueItem, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, event_type, to_number, from_number, payload, created_at, next_retry_at, retry_count, status, size_bytes
		FROM command_queue
		WHERE status IN ('pending', 'sending')
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("flush query: %w", err)
	}
	defer rows.Close()

	var items []CommandQueueItem
	for rows.Next() {
		var item CommandQueueItem
		if err := rows.Scan(&item.ID, &item.EventType, &item.ToNumber, &item.FromNumber,
			&item.Payload, &item.CreatedAt, &item.NextRetryAt, &item.RetryCount,
			&item.Status, &item.SizeBytes); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Exists checks whether an event ID is already in the queue (for dedup).
func (q *CommandQueue) Exists(ctx context.Context, eventID string) (bool, error) {
	var exists int
	err := q.db.QueryRowContext(ctx, "SELECT 1 FROM command_queue WHERE id = ?", eventID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("dedup check: %w", err)
	}
	return true, nil
}

// SetMaxSize sets the maximum queue size in bytes.
func (q *CommandQueue) SetMaxSize(bytes int64) {
	q.mu.Lock()
	q.maxSizeBytes = bytes
	q.mu.Unlock()
}

// Metrics returns current queue metrics.
type QueueMetrics struct {
	DepthBytes  int64 `json:"depth_bytes"`
	DepthItems  int64 `json:"depth_items"`
	FlushCount  int64 `json:"flush_count"`
	MaxSizeMB   int64 `json:"max_size_mb"`
}

func (q *CommandQueue) Metrics() QueueMetrics {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return QueueMetrics{
		DepthBytes: q.sizeBytes,
		DepthItems: q.itemCount,
		FlushCount: q.flushCount,
		MaxSizeMB:  q.maxSizeBytes / (1024 * 1024),
	}
}

// PayloadHash computes a SHA-256 hash of the payload for dedup comparison.
func PayloadHash(payload string) string {
	h := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", h)
}

// Close closes the SQLite database connection.
func (q *CommandQueue) Close() error {
	return q.db.Close()
}

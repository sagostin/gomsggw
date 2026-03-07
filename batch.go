package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// BatchJob tracks a batch sending job.
type BatchJob struct {
	ID           string     `gorm:"primaryKey" json:"id"` // UUID
	ClientID     uint       `gorm:"index;not null" json:"client_id"`
	APIKeyID     *uint      `json:"api_key_id,omitempty"`
	Status       string     `gorm:"default:'pending'" json:"status"` // "pending", "processing", "partially_queued", "completed", "failed"
	TotalCount   int        `json:"total_count"`
	SentCount    int        `json:"sent_count"`
	FailedCount  int        `json:"failed_count"`
	QueuedCount  int        `json:"queued_count"` // Messages waiting for limit reset
	FromNumber   string     `json:"from_number"`
	ErrorsJSON   string     `gorm:"column:errors;type:text" json:"-"` // JSON array of per-message errors
	WebhookURL   string     `json:"webhook_url,omitempty"`
	ThrottleRPS  int        `json:"throttle_per_second"` // Messages per second (0 = no throttle)
	MaxRetryMins int        `json:"max_retry_mins"`      // Max minutes to retry queued msgs (0 = 60 default)
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// BatchMessageItem tracks each individual message within a batch job.
type BatchMessageItem struct {
	ID         string     `gorm:"primaryKey" json:"id"` // UUID — the cancellable message ID
	BatchJobID string     `gorm:"index;not null" json:"batch_job_id"`
	Index      int        `json:"index"` // Position in original batch
	To         string     `json:"to"`
	Text       string     `json:"text"`
	Status     string     `gorm:"default:'pending'" json:"status"` // pending, sent, queued, failed, cancelled
	Error      string     `json:"error,omitempty"`
	ErrorCode  int        `json:"error_code,omitempty"`
	QueuedAt   *time.Time `json:"queued_at,omitempty"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// BatchMessageError tracks a per-message error within a batch (for legacy JSON errors field).
type BatchMessageError struct {
	Index int    `json:"index"`
	To    string `json:"to"`
	Error string `json:"error"`
	Code  int    `json:"code"` // HTTP-style status code (429, 403, etc.)
}

// BatchMessage is a single message within a batch request.
type BatchMessage struct {
	To        string            `json:"to"`
	Text      string            `json:"text,omitempty"`
	Variables map[string]string `json:"variables,omitempty"` // Template variables
}

// BatchRequest is the JSON request body for POST /messages/batch.
type BatchRequest struct {
	From           string         `json:"from"`
	Messages       []BatchMessage `json:"messages"`
	TextTemplate   string         `json:"text_template,omitempty"` // Shared template with {{var}} placeholders
	WebhookURL     string         `json:"webhook_url,omitempty"`
	ThrottlePerSec int            `json:"throttle_per_second,omitempty"`
	MaxRetryMins   int            `json:"max_retry_mins,omitempty"` // Max minutes to retry queued msgs
}

// BatchResponse is returned on batch submission.
type BatchResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	TotalCount int    `json:"total_count"`
}

// BatchStatusResponse is the full status of a batch job.
type BatchStatusResponse struct {
	BatchJob
	Errors []BatchMessageError `json:"errors,omitempty"`
}

// GetErrors deserializes the JSON errors string.
func (b *BatchJob) GetErrors() []BatchMessageError {
	if b.ErrorsJSON == "" {
		return nil
	}
	var errors []BatchMessageError
	json.Unmarshal([]byte(b.ErrorsJSON), &errors)
	return errors
}

// SetErrors serializes errors to JSON.
func (b *BatchJob) SetErrors(errors []BatchMessageError) {
	data, _ := json.Marshal(errors)
	b.ErrorsJSON = string(data)
}

// ApplyTemplate replaces {{key}} placeholders in a template with variable values.
func ApplyTemplate(template string, variables map[string]string) string {
	result := template
	for key, value := range variables {
		placeholder := "{{" + key + "}}"
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}

// ParseCSVBatch reads a CSV file and produces BatchMessages.
// Expected columns: "to" (required), plus any variable columns.
// An optional "text" column provides per-row text (overrides template).
func ParseCSVBatch(reader io.Reader) ([]BatchMessage, error) {
	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true

	// Read header
	header, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Find column indices
	toIdx := -1
	textIdx := -1
	varCols := make(map[int]string) // col index -> variable name

	for i, col := range header {
		normalized := strings.TrimSpace(strings.ToLower(col))
		switch normalized {
		case "to", "phone", "number", "destination":
			toIdx = i
		case "text", "message", "body":
			textIdx = i
		default:
			varCols[i] = strings.TrimSpace(col)
		}
	}

	if toIdx == -1 {
		return nil, fmt.Errorf("CSV must have a 'to' (or 'phone'/'number'/'destination') column")
	}

	// Read rows
	var messages []BatchMessage
	lineNum := 1 // header was line 1
	for {
		lineNum++
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV parse error on line %d: %w", lineNum, err)
		}

		if toIdx >= len(record) {
			continue // skip malformed rows
		}

		to := strings.TrimSpace(record[toIdx])
		if to == "" {
			continue // skip empty destinations
		}

		msg := BatchMessage{
			To:        to,
			Variables: make(map[string]string),
		}

		if textIdx >= 0 && textIdx < len(record) {
			msg.Text = strings.TrimSpace(record[textIdx])
		}

		for colIdx, varName := range varCols {
			if colIdx < len(record) {
				msg.Variables[varName] = strings.TrimSpace(record[colIdx])
			}
		}

		messages = append(messages, msg)
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("CSV contains no valid message rows")
	}

	return messages, nil
}

// processBatchJob runs in a background goroutine to send all messages in a batch.
// Rate-limited messages are queued for retry instead of permanently failed.
func (gateway *Gateway) processBatchJob(job *BatchJob, messages []BatchMessage, textTemplate string, client *Client) {
	lm := gateway.LogManager

	lm.SendLog(lm.BuildLog(
		"Batch.Process",
		"Starting batch job",
		logrus.InfoLevel,
		map[string]interface{}{
			"jobID":      job.ID,
			"clientID":   job.ClientID,
			"totalCount": job.TotalCount,
			"fromNumber": job.FromNumber,
			"throttle":   job.ThrottleRPS,
		},
	))

	// Update status to processing
	job.Status = "processing"
	gateway.DB.Model(job).Update("status", "processing")

	var mu sync.Mutex

	// Create BatchMessageItems for all messages
	var items []BatchMessageItem
	for i, msg := range messages {
		// Determine message text
		text := msg.Text
		if text == "" && textTemplate != "" {
			text = ApplyTemplate(textTemplate, msg.Variables)
		}

		// Normalize destination
		toNumber := msg.To
		if normalized, err := FormatToE164(msg.To); err == nil {
			toNumber = normalized
		}

		items = append(items, BatchMessageItem{
			ID:         uuid.New().String(),
			BatchJobID: job.ID,
			Index:      i,
			To:         toNumber,
			Text:       text,
			Status:     "pending",
			CreatedAt:  time.Now(),
		})
	}

	// Bulk insert message items
	gateway.DB.CreateInBatches(&items, 100)

	// Throttle ticker
	var ticker *time.Ticker
	if job.ThrottleRPS > 0 {
		interval := time.Second / time.Duration(job.ThrottleRPS)
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
	}

	// === Initial pass: send what we can, queue what's limited ===
	for idx := range items {
		item := &items[idx]

		// Throttle
		if ticker != nil {
			<-ticker.C
		}

		// Check if cancelled while processing
		var freshItem BatchMessageItem
		gateway.DB.Select("status").Where("id = ?", item.ID).First(&freshItem)
		if freshItem.Status == "cancelled" {
			continue
		}

		// Validate text
		if item.Text == "" {
			item.Status = "failed"
			item.Error = "no message text (no per-message text and no template)"
			item.ErrorCode = 400
			gateway.DB.Model(item).Updates(map[string]interface{}{
				"status": item.Status, "error": item.Error, "error_code": item.ErrorCode,
			})
			mu.Lock()
			job.FailedCount++
			mu.Unlock()
			continue
		}

		// Validate destination
		if _, err := FormatToE164(item.To); err != nil {
			item.Status = "failed"
			item.Error = fmt.Sprintf("invalid destination number: %v", err)
			item.ErrorCode = 400
			gateway.DB.Model(item).Updates(map[string]interface{}{
				"status": item.Status, "error": item.Error, "error_code": item.ErrorCode,
			})
			mu.Lock()
			job.FailedCount++
			mu.Unlock()
			continue
		}

		// Check limits
		limitResult := gateway.CheckMessageLimits(client, job.FromNumber, "sms", "outbound")
		if limitResult != nil && !limitResult.Allowed {
			// Queue for retry instead of failing
			now := time.Now()
			item.Status = "queued"
			item.Error = limitResult.Message
			item.ErrorCode = 429
			item.QueuedAt = &now
			gateway.DB.Model(item).Updates(map[string]interface{}{
				"status": "queued", "error": item.Error, "error_code": 429, "queued_at": now,
			})
			mu.Lock()
			job.QueuedCount++
			mu.Unlock()
			continue
		}

		// Send the message
		gateway.sendBatchMessage(item, job)

		mu.Lock()
		job.SentCount++
		mu.Unlock()

		// Periodically update DB progress (every 100 messages)
		if (idx+1)%100 == 0 {
			mu.Lock()
			gateway.DB.Model(job).Updates(map[string]interface{}{
				"sent_count":   job.SentCount,
				"failed_count": job.FailedCount,
				"queued_count": job.QueuedCount,
			})
			mu.Unlock()
		}
	}

	// Update counts after initial pass
	gateway.DB.Model(job).Updates(map[string]interface{}{
		"sent_count":   job.SentCount,
		"failed_count": job.FailedCount,
		"queued_count": job.QueuedCount,
	})

	// === Retry loop for queued messages ===
	if job.QueuedCount > 0 {
		job.Status = "partially_queued"
		gateway.DB.Model(job).Update("status", "partially_queued")

		maxRetry := time.Duration(job.MaxRetryMins) * time.Minute
		if maxRetry <= 0 {
			maxRetry = 60 * time.Minute // default 1 hour
		}
		retryDeadline := time.Now().Add(maxRetry)
		retryInterval := 30 * time.Second

		lm.SendLog(lm.BuildLog(
			"Batch.Retry",
			"Entering retry loop for queued messages",
			logrus.InfoLevel,
			map[string]interface{}{
				"jobID":       job.ID,
				"queuedCount": job.QueuedCount,
				"retryUntil":  retryDeadline.Format(time.RFC3339),
			},
		))

		for time.Now().Before(retryDeadline) && job.QueuedCount > 0 {
			time.Sleep(retryInterval)

			// Load currently queued items
			var queuedItems []BatchMessageItem
			gateway.DB.Where("batch_job_id = ? AND status = 'queued'", job.ID).Find(&queuedItems)

			if len(queuedItems) == 0 {
				job.QueuedCount = 0
				break
			}

			for idx := range queuedItems {
				qi := &queuedItems[idx]

				// Re-check if it was cancelled during wait
				var check BatchMessageItem
				gateway.DB.Select("status").Where("id = ?", qi.ID).First(&check)
				if check.Status == "cancelled" {
					mu.Lock()
					job.QueuedCount--
					mu.Unlock()
					continue
				}

				// Re-check limits
				limitResult := gateway.CheckMessageLimits(client, job.FromNumber, "sms", "outbound")
				if limitResult != nil && !limitResult.Allowed {
					continue // Still rate limited, keep queued
				}

				// Throttle
				if ticker != nil {
					<-ticker.C
				}

				// Send the message
				gateway.sendBatchMessage(qi, job)

				mu.Lock()
				job.SentCount++
				job.QueuedCount--
				mu.Unlock()
			}

			// Update DB with retry progress
			gateway.DB.Model(job).Updates(map[string]interface{}{
				"sent_count":   job.SentCount,
				"queued_count": job.QueuedCount,
			})
		}

		// Expire remaining queued messages after deadline
		if job.QueuedCount > 0 {
			var remaining []BatchMessageItem
			gateway.DB.Where("batch_job_id = ? AND status = 'queued'", job.ID).Find(&remaining)
			for idx := range remaining {
				remaining[idx].Status = "failed"
				remaining[idx].Error = "retry deadline exceeded"
				remaining[idx].ErrorCode = 429
				gateway.DB.Model(&remaining[idx]).Updates(map[string]interface{}{
					"status": "failed", "error": "retry deadline exceeded", "error_code": 429,
				})
			}
			mu.Lock()
			job.FailedCount += len(remaining)
			job.QueuedCount = 0
			mu.Unlock()
		}
	}

	// === Finalize ===
	now := time.Now()
	job.CompletedAt = &now
	job.QueuedCount = 0

	// Recount from DB for accuracy (in case of cancels during processing)
	var sentCount, failedCount, cancelledCount int64
	gateway.DB.Model(&BatchMessageItem{}).Where("batch_job_id = ? AND status = 'sent'", job.ID).Count(&sentCount)
	gateway.DB.Model(&BatchMessageItem{}).Where("batch_job_id = ? AND status = 'failed'", job.ID).Count(&failedCount)
	gateway.DB.Model(&BatchMessageItem{}).Where("batch_job_id = ? AND status = 'cancelled'", job.ID).Count(&cancelledCount)

	job.SentCount = int(sentCount)
	job.FailedCount = int(failedCount + cancelledCount)

	if job.SentCount == 0 {
		job.Status = "failed"
	} else {
		job.Status = "completed"
	}

	// Build errors list from failed items
	var failedItems []BatchMessageItem
	gateway.DB.Where("batch_job_id = ? AND status IN ('failed','cancelled')", job.ID).Find(&failedItems)
	var batchErrors []BatchMessageError
	for _, fi := range failedItems {
		batchErrors = append(batchErrors, BatchMessageError{
			Index: fi.Index,
			To:    fi.To,
			Error: fi.Error,
			Code:  fi.ErrorCode,
		})
	}
	job.SetErrors(batchErrors)

	gateway.DB.Model(job).Updates(map[string]interface{}{
		"status":       job.Status,
		"sent_count":   job.SentCount,
		"failed_count": job.FailedCount,
		"queued_count": 0,
		"errors":       job.ErrorsJSON,
		"completed_at": job.CompletedAt,
	})

	lm.SendLog(lm.BuildLog(
		"Batch.Process",
		"Batch job completed",
		logrus.InfoLevel,
		map[string]interface{}{
			"jobID":       job.ID,
			"status":      job.Status,
			"sentCount":   job.SentCount,
			"failedCount": job.FailedCount,
			"duration":    time.Since(job.CreatedAt).String(),
		},
	))

	// Fire webhook callback if configured
	if job.WebhookURL != "" {
		go gateway.sendBatchWebhook(job)
	}
}

// sendBatchMessage sends a single batch message item to the router.
func (gateway *Gateway) sendBatchMessage(item *BatchMessageItem, job *BatchJob) {
	now := time.Now()
	queueItem := MsgQueueItem{
		From:              job.FromNumber,
		To:                item.To,
		message:           item.Text,
		Type:              MsgQueueItemType.SMS,
		ReceivedTimestamp: now,
		LogID:             item.ID, // Use the message item ID as the log ID
	}

	gateway.Router.ClientMsgChan <- queueItem

	item.Status = "sent"
	item.SentAt = &now
	gateway.DB.Model(item).Updates(map[string]interface{}{
		"status": "sent", "sent_at": now, "error": "", "error_code": 0,
	})
}

// sendBatchWebhook POSTs the batch job status to the configured webhook URL.
func (gateway *Gateway) sendBatchWebhook(job *BatchJob) {
	payload := BatchStatusResponse{
		BatchJob: *job,
		Errors:   job.GetErrors(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"Batch.Webhook",
			"Failed to marshal webhook payload",
			logrus.ErrorLevel,
			map[string]interface{}{
				"jobID": job.ID,
				"error": err.Error(),
			},
		))
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(job.WebhookURL, "application/json", strings.NewReader(string(data)))
	if err != nil {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"Batch.Webhook",
			"Webhook delivery failed",
			logrus.WarnLevel,
			map[string]interface{}{
				"jobID": job.ID,
				"url":   job.WebhookURL,
				"error": err.Error(),
			},
		))
		return
	}
	defer resp.Body.Close()

	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"Batch.Webhook",
		"Webhook delivered",
		logrus.InfoLevel,
		map[string]interface{}{
			"jobID":      job.ID,
			"url":        job.WebhookURL,
			"statusCode": resp.StatusCode,
		},
	))
}

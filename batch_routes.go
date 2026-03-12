package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
)

// requireBatchScope is a helper that checks if an API key (if present) has the "batch" scope.
// Returns true if the request should be blocked (scope missing). Writes the error response.
func requireBatchScope(ctx iris.Context) bool {
	apiKeyVal := ctx.Values().Get("api_key")
	if apiKeyVal != nil {
		apiKey := apiKeyVal.(*TenantAPIKey)
		if !apiKey.HasScope("batch") {
			ctx.StatusCode(iris.StatusForbidden)
			ctx.JSON(iris.Map{"error": "API key does not have 'batch' scope"})
			return true
		}
	}
	return false
}

// SetupAPIKeyRoutes sets up admin endpoints for managing tenant API keys.
// These are under /clients/{id}/api-keys and require admin auth (basicAuthMiddleware).
func SetupAPIKeyRoutes(app *iris.Application, gateway *Gateway) {
	apiKeys := app.Party("/clients/{client_id}/api-keys", gateway.basicAuthMiddleware)
	{
		// POST /clients/{client_id}/api-keys - Create a new API key
		apiKeys.Post("/", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("client_id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			var req struct {
				Name             string `json:"name"`
				Scopes           string `json:"scopes"`             // "send,batch,usage"
				RateLimit        int    `json:"rate_limit"`         // 0 = use client limit
				ExpiresInDays    int    `json:"expires_in_days"`    // 0 = never expires
				AllowedNumberIDs []uint `json:"allowed_number_ids"` // Empty = all numbers
			}
			if err := ctx.ReadJSON(&req); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid request body"})
				return
			}

			if req.Name == "" {
				req.Name = "API Key"
			}
			if req.Scopes == "" {
				req.Scopes = "send,batch,usage"
			}

			var expiresAt *time.Time
			if req.ExpiresInDays > 0 {
				t := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
				expiresAt = &t
			}

			rawKey, apiKey, err := gateway.createAPIKey(uint(clientID), req.Name, req.Scopes, req.RateLimit, expiresAt, req.AllowedNumberIDs)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": err.Error()})
				return
			}

			gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
				"Admin.APIKeys",
				"Created API key",
				logrus.InfoLevel,
				map[string]interface{}{
					"clientID":  clientID,
					"keyName":   req.Name,
					"keyPrefix": apiKey.KeyPrefix,
					"scopes":    req.Scopes,
				},
			))

			ctx.StatusCode(iris.StatusCreated)
			ctx.JSON(iris.Map{
				"key":             rawKey, // Only returned once!
				"id":              apiKey.ID,
				"name":            apiKey.Name,
				"key_prefix":      apiKey.KeyPrefix,
				"scopes":          apiKey.Scopes,
				"rate_limit":      apiKey.RateLimit,
				"active":          apiKey.Active,
				"expires_at":      apiKey.ExpiresAt,
				"allowed_numbers": apiKey.AllowedNumbers,
				"created_at":      apiKey.CreatedAt,
			})
		})

		// GET /clients/{client_id}/api-keys - List API keys
		apiKeys.Get("/", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("client_id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			keys, err := gateway.listAPIKeys(uint(clientID))
			if err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to list API keys"})
				return
			}

			ctx.JSON(keys)
		})

		// DELETE /clients/{client_id}/api-keys/{key_id} - Revoke an API key
		apiKeys.Delete("/{key_id}", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("client_id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			keyIDStr := ctx.Params().Get("key_id")
			keyID, err := strconv.ParseUint(keyIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid key ID"})
				return
			}

			if err := gateway.revokeAPIKey(uint(keyID), uint(clientID)); err != nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": err.Error()})
				return
			}

			gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
				"Admin.APIKeys",
				"Revoked API key",
				logrus.InfoLevel,
				map[string]interface{}{
					"clientID": clientID,
					"keyID":    keyID,
				},
			))

			ctx.JSON(iris.Map{"message": "API key revoked", "key_id": keyID})
		})
	}
}

// SetupBatchRoutes sets up the HTTP routes for batch message sending.
// These use clientAuthMiddleware so both client credentials and API keys work.
func SetupBatchRoutes(app *iris.Application, gateway *Gateway) {
	batch := app.Party("/messages/batch", gateway.clientAuthMiddleware)
	{
		// POST /messages/batch - Submit a batch job
		batch.Post("/", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			lm := gateway.LogManager
			client := ctx.Values().Get("client").(*Client)

			// Get API key if present (for number scoping)
			apiKeyVal := ctx.Values().Get("api_key")
			var apiKey *TenantAPIKey
			if apiKeyVal != nil {
				apiKey = apiKeyVal.(*TenantAPIKey)
			}

			// Parse request
			var req BatchRequest
			contentType := ctx.GetHeader("Content-Type")

			if strings.HasPrefix(contentType, "multipart/form-data") {
				// CSV upload
				fromNumber := ctx.FormValue("from")
				textTemplate := ctx.FormValue("text_template")
				webhookURL := ctx.FormValue("webhook_url")
				throttleStr := ctx.FormValue("throttle_per_second")

				file, _, err := ctx.FormFile("csv")
				if err != nil {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "CSV file is required for multipart upload"})
					return
				}
				defer file.Close()

				messages, err := ParseCSVBatch(file)
				if err != nil {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": err.Error()})
					return
				}

				req.From = fromNumber
				req.Messages = messages
				req.TextTemplate = textTemplate
				req.WebhookURL = webhookURL
				if throttleStr != "" {
					throttle, _ := strconv.Atoi(throttleStr)
					req.ThrottlePerSec = throttle
				}
			} else {
				// JSON request
				if err := ctx.ReadJSON(&req); err != nil {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "Invalid request body"})
					return
				}
			}

			// Validate
			if len(req.Messages) == 0 {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "No messages provided"})
				return
			}

			if len(req.Messages) > 10000 {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Batch size exceeds maximum of 10,000 messages"})
				return
			}

			// Resolve from number
			fromNumber := req.From
			if fromNumber == "" {
				if len(client.Numbers) == 1 {
					fromNumber = client.Numbers[0].Number
				} else {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "'from' field is required when client has multiple numbers"})
					return
				}
			}

			// Verify from number ownership
			owned := false
			for _, n := range client.Numbers {
				if n.Number == fromNumber || strings.TrimPrefix(n.Number, "+") == strings.TrimPrefix(fromNumber, "+") {
					owned = true
					break
				}
			}
			if !owned {
				ctx.StatusCode(iris.StatusForbidden)
				ctx.JSON(iris.Map{"error": "Client does not own the specified 'from' number"})
				return
			}

			// API key number scoping check
			if apiKey != nil && !apiKey.IsNumberAllowed(fromNumber) {
				ctx.StatusCode(iris.StatusForbidden)
				ctx.JSON(iris.Map{"error": "API key is not authorized to send from this number"})
				return
			}

			// Set defaults
			throttle := req.ThrottlePerSec
			if throttle <= 0 {
				throttle = 30 // default 30 msg/sec
			}

			// Create batch job
			var apiKeyID *uint
			if apiKey != nil {
				apiKeyID = &apiKey.ID
			}

			job := &BatchJob{
				ID:           uuid.New().String(),
				ClientID:     client.ID,
				APIKeyID:     apiKeyID,
				Status:       "pending",
				TotalCount:   len(req.Messages),
				FromNumber:   fromNumber,
				WebhookURL:   req.WebhookURL,
				ThrottleRPS:  throttle,
				MaxRetryMins: req.MaxRetryMins,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}

			if err := gateway.DB.Create(job).Error; err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to create batch job"})
				return
			}

			lm.SendLog(lm.BuildLog(
				"Batch.Submit",
				"Batch job created",
				logrus.InfoLevel,
				map[string]interface{}{
					"jobID":      job.ID,
					"clientID":   client.ID,
					"totalCount": job.TotalCount,
					"fromNumber": fromNumber,
					"throttle":   throttle,
				},
			))

			// Start processing in background
			go gateway.processBatchJob(job, req.Messages, req.TextTemplate, client)

			ctx.StatusCode(iris.StatusAccepted)
			ctx.JSON(BatchResponse{
				ID:         job.ID,
				Status:     job.Status,
				TotalCount: job.TotalCount,
			})
		})

		// POST /messages/batch/check - Pre-check batch limits before sending
		batch.Post("/check", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			client := ctx.Values().Get("client").(*Client)

			var req struct {
				From         string `json:"from"`
				MessageCount int    `json:"message_count"`
				MsgType      string `json:"msg_type"` // "sms" or "mms", default "sms"
			}
			if err := ctx.ReadJSON(&req); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid request body"})
				return
			}

			if req.MessageCount <= 0 {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "'message_count' must be greater than 0"})
				return
			}

			if req.MsgType == "" {
				req.MsgType = "sms"
			}

			// Resolve from number
			fromNumber := req.From
			if fromNumber == "" {
				if len(client.Numbers) == 1 {
					fromNumber = client.Numbers[0].Number
				} else {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "'from' field is required when client has multiple numbers"})
					return
				}
			}

			// Verify ownership
			owned := false
			for _, n := range client.Numbers {
				if n.Number == fromNumber || strings.TrimPrefix(n.Number, "+") == strings.TrimPrefix(fromNumber, "+") {
					owned = true
					break
				}
			}
			if !owned {
				ctx.StatusCode(iris.StatusForbidden)
				ctx.JSON(iris.Map{"error": "Client does not own the specified 'from' number"})
				return
			}

			// API key number scoping
			apiKeyVal := ctx.Values().Get("api_key")
			if apiKeyVal != nil {
				apiKey := apiKeyVal.(*TenantAPIKey)
				if !apiKey.IsNumberAllowed(fromNumber) {
					ctx.StatusCode(iris.StatusForbidden)
					ctx.JSON(iris.Map{"error": "API key is not authorized to send from this number"})
					return
				}
			}

			// Build limit info for each period
			buildPeriodInfo := func(period string) iris.Map {
				periodStart := GetPeriodStart(client, period)
				used, _ := gateway.GetUsageCountByType(client.ID, "", req.MsgType, periodStart)

				var limit int64
				if client.Settings != nil {
					limit, _, _ = getEffectiveLimit(client.Settings, nil, req.MsgType, period)
				}

				var remaining interface{} = nil
				if limit > 0 {
					rem := limit - used
					if rem < 0 {
						rem = 0
					}
					remaining = rem
				}

				return iris.Map{
					"current_usage": used,
					"limit":         limit,
					"remaining":     remaining,
				}
			}

			burstInfo := buildPeriodInfo("burst")
			dailyInfo := buildPeriodInfo("daily")
			monthlyInfo := buildPeriodInfo("monthly")

			// Per-number limits
			var numberInfo iris.Map
			for _, n := range client.Numbers {
				if n.Number == fromNumber || strings.TrimPrefix(n.Number, "+") == strings.TrimPrefix(fromNumber, "+") {
					dailyStart := GetPeriodStart(client, "daily")
					numUsed, _ := gateway.GetUsageCountWithDirection(client.ID, n.Number, req.MsgType, "outbound", dailyStart)
					var numLimit int64
					if n.Settings != nil {
						numLimit, _, _ = getEffectiveLimit(client.Settings, n.Settings, req.MsgType, "daily")
					}
					var numRemaining interface{} = nil
					if numLimit > 0 {
						rem := numLimit - numUsed
						if rem < 0 {
							rem = 0
						}
						numRemaining = rem
					}
					numberInfo = iris.Map{
						"number":        n.Number,
						"current_usage": numUsed,
						"limit":         numLimit,
						"remaining":     numRemaining,
					}
					break
				}
			}

			// Determine if the batch can proceed
			allowed := true
			var blockingReason string

			// Check each period's remaining against message_count
			checkRemaining := func(info iris.Map, period string) {
				if rem, ok := info["remaining"]; ok && rem != nil {
					if remInt, ok := rem.(int64); ok && remInt < int64(req.MessageCount) {
						allowed = false
						blockingReason = fmt.Sprintf("%s %s limit would be exceeded (%d remaining, %d requested)", req.MsgType, period, remInt, req.MessageCount)
					}
				}
			}
			checkRemaining(burstInfo, "burst")
			checkRemaining(dailyInfo, "daily")
			checkRemaining(monthlyInfo, "monthly")

			if numberInfo != nil {
				if rem, ok := numberInfo["remaining"]; ok && rem != nil {
					if remInt, ok := rem.(int64); ok && remInt < int64(req.MessageCount) {
						allowed = false
						blockingReason = fmt.Sprintf("number daily limit would be exceeded (%d remaining, %d requested)", remInt, req.MessageCount)
					}
				}
			}

			result := iris.Map{
				"allowed":       allowed,
				"message_count": req.MessageCount,
				"msg_type":      req.MsgType,
				"from":          fromNumber,
				"limits": iris.Map{
					"burst":   burstInfo,
					"daily":   dailyInfo,
					"monthly": monthlyInfo,
				},
			}

			if numberInfo != nil {
				result["number_limit"] = numberInfo
			}

			if !allowed {
				result["reason"] = blockingReason
			}

			ctx.JSON(result)
		})

		// GET /messages/batch/{id} - Get batch job status
		batch.Get("/{id}", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			jobID := ctx.Params().Get("id")
			client := ctx.Values().Get("client").(*Client)

			var job BatchJob
			if err := gateway.DB.Where("id = ? AND client_id = ?", jobID, client.ID).First(&job).Error; err != nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Batch job not found"})
				return
			}

			ctx.JSON(BatchStatusResponse{
				BatchJob: job,
				Errors:   job.GetErrors(),
			})
		})

		// GET /messages/batch - List recent batch jobs (with pagination and filtering)
		batch.Get("/", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			client := ctx.Values().Get("client").(*Client)

			// Pagination
			page, _ := strconv.Atoi(ctx.URLParamDefault("page", "1"))
			perPage, _ := strconv.Atoi(ctx.URLParamDefault("per_page", "50"))
			if page < 1 {
				page = 1
			}
			if perPage < 1 {
				perPage = 50
			}
			if perPage > 100 {
				perPage = 100
			}
			offset := (page - 1) * perPage

			// Build query with filters
			query := gateway.DB.Where("client_id = ?", client.ID)

			// Status filter
			if status := ctx.URLParam("status"); status != "" {
				query = query.Where("status = ?", status)
			}

			// From number filter
			if from := ctx.URLParam("from"); from != "" {
				cleanFrom := strings.TrimPrefix(from, "+")
				query = query.Where("from_number = ? OR from_number = ?", from, cleanFrom)
			}

			// Date filter
			if since := ctx.URLParam("since"); since != "" {
				if t, err := time.Parse("2006-01-02", since); err == nil {
					query = query.Where("created_at >= ?", t)
				} else if t, err := time.Parse(time.RFC3339, since); err == nil {
					query = query.Where("created_at >= ?", t)
				}
			}
			if until := ctx.URLParam("until"); until != "" {
				if t, err := time.Parse("2006-01-02", until); err == nil {
					query = query.Where("created_at <= ?", t.Add(24*time.Hour))
				} else if t, err := time.Parse(time.RFC3339, until); err == nil {
					query = query.Where("created_at <= ?", t)
				}
			}

			// Total count
			var totalCount int64
			query.Model(&BatchJob{}).Count(&totalCount)

			// Fetch page
			var jobs []BatchJob
			query.Order("created_at DESC").
				Offset(offset).
				Limit(perPage).
				Find(&jobs)

			ctx.Header("X-Total-Count", strconv.FormatInt(totalCount, 10))
			ctx.Header("X-Page", strconv.Itoa(page))
			ctx.Header("X-Per-Page", strconv.Itoa(perPage))

			ctx.JSON(jobs)
		})

		// GET /messages/batch/{id}/messages - List all messages in a batch job (with pagination)
		batch.Get("/{id}/messages", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			jobID := ctx.Params().Get("id")
			client := ctx.Values().Get("client").(*Client)

			// Verify job belongs to client
			var job BatchJob
			if err := gateway.DB.Where("id = ? AND client_id = ?", jobID, client.ID).First(&job).Error; err != nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Batch job not found"})
				return
			}

			// Pagination
			page, _ := strconv.Atoi(ctx.URLParamDefault("page", "1"))
			perPage, _ := strconv.Atoi(ctx.URLParamDefault("per_page", "100"))
			if page < 1 {
				page = 1
			}
			if perPage < 1 {
				perPage = 100
			}
			if perPage > 500 {
				perPage = 500
			}
			offset := (page - 1) * perPage

			// Build query
			query := gateway.DB.Where("batch_job_id = ?", jobID)

			// Optional status filter
			if statusFilter := ctx.URLParam("status"); statusFilter != "" {
				query = query.Where("status = ?", statusFilter)
			}

			// Total count
			var totalCount int64
			query.Model(&BatchMessageItem{}).Count(&totalCount)

			// Fetch page
			var items []BatchMessageItem
			query.Order("\"index\" ASC").
				Offset(offset).
				Limit(perPage).
				Find(&items)

			ctx.Header("X-Total-Count", strconv.FormatInt(totalCount, 10))
			ctx.Header("X-Page", strconv.Itoa(page))
			ctx.Header("X-Per-Page", strconv.Itoa(perPage))

			ctx.JSON(items)
		})

		// POST /messages/batch/{id}/cancel - Cancel an entire batch job
		batch.Post("/{id}/cancel", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			jobID := ctx.Params().Get("id")
			client := ctx.Values().Get("client").(*Client)

			// Verify job belongs to client
			var job BatchJob
			if err := gateway.DB.Where("id = ? AND client_id = ?", jobID, client.ID).First(&job).Error; err != nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Batch job not found"})
				return
			}

			// Only active jobs can be cancelled
			if job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled" {
				ctx.StatusCode(iris.StatusConflict)
				ctx.JSON(iris.Map{
					"error":  "Batch job cannot be cancelled",
					"status": job.Status,
					"detail": fmt.Sprintf("Job is already '%s'", job.Status),
				})
				return
			}

			// Cancel all pending and queued messages
			result := gateway.DB.Model(&BatchMessageItem{}).
				Where("batch_job_id = ? AND status IN ('pending', 'queued')", jobID).
				Updates(map[string]interface{}{
					"status": "cancelled",
					"error":  "batch job cancelled by user",
				})

			cancelledCount := int(result.RowsAffected)

			// Recount final stats
			var sentCount, failedCount, totalCancelled int64
			gateway.DB.Model(&BatchMessageItem{}).Where("batch_job_id = ? AND status = 'sent'", jobID).Count(&sentCount)
			gateway.DB.Model(&BatchMessageItem{}).Where("batch_job_id = ? AND status = 'failed'", jobID).Count(&failedCount)
			gateway.DB.Model(&BatchMessageItem{}).Where("batch_job_id = ? AND status = 'cancelled'", jobID).Count(&totalCancelled)

			now := time.Now()
			gateway.DB.Model(&job).Updates(map[string]interface{}{
				"status":       "cancelled",
				"sent_count":   int(sentCount),
				"failed_count": int(failedCount + totalCancelled),
				"queued_count": 0,
				"completed_at": now,
			})

			gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
				"Batch.Cancel",
				"Batch job cancelled",
				logrus.InfoLevel,
				map[string]interface{}{
					"jobID":          jobID,
					"clientID":       client.ID,
					"cancelledCount": cancelledCount,
				},
			))

			ctx.JSON(iris.Map{
				"message":         "Batch job cancelled",
				"job_id":          jobID,
				"status":          "cancelled",
				"cancelled_count": cancelledCount,
				"sent_count":      int(sentCount),
			})
		})

		// DELETE /messages/batch/{id}/messages/{msg_id} - Cancel a queued/pending message
		batch.Delete("/{id}/messages/{msg_id}", func(ctx iris.Context) {
			if requireBatchScope(ctx) {
				return
			}

			jobID := ctx.Params().Get("id")
			msgID := ctx.Params().Get("msg_id")
			client := ctx.Values().Get("client").(*Client)

			// Verify job belongs to client
			var job BatchJob
			if err := gateway.DB.Where("id = ? AND client_id = ?", jobID, client.ID).First(&job).Error; err != nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Batch job not found"})
				return
			}

			// Find the message item
			var item BatchMessageItem
			if err := gateway.DB.Where("id = ? AND batch_job_id = ?", msgID, jobID).First(&item).Error; err != nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Message not found"})
				return
			}

			// Only pending or queued messages can be cancelled
			if item.Status != "pending" && item.Status != "queued" {
				ctx.StatusCode(iris.StatusConflict)
				ctx.JSON(iris.Map{
					"error":  "Message cannot be cancelled",
					"status": item.Status,
					"detail": fmt.Sprintf("Message is already '%s'", item.Status),
				})
				return
			}

			// Cancel the message
			gateway.DB.Model(&item).Updates(map[string]interface{}{
				"status": "cancelled",
				"error":  "cancelled by user",
			})

			gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
				"Batch.Cancel",
				"Batch message cancelled",
				logrus.InfoLevel,
				map[string]interface{}{
					"jobID":    jobID,
					"msgID":    msgID,
					"clientID": client.ID,
					"to":       item.To,
				},
			))

			ctx.JSON(iris.Map{
				"message":    "Message cancelled",
				"message_id": msgID,
				"status":     "cancelled",
			})
		})
	}
}

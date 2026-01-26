package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
)

// PasswordUpdateRequest defines the expected JSON request body
type PasswordUpdateRequest struct {
	NewPassword string `json:"new_password"`
}

// StatsResponse represents the overall statistics response.
type StatsResponse struct {
	SMPPConnectedClients int              `json:"smpp_connected_clients"`
	SMPPClients          []SMPPClientInfo `json:"smpp_clients"`
	MM4ConnectedClients  int              `json:"mm4_connected_clients"`
	MM4Clients           []MM4ClientInfo  `json:"mm4_clients"`
}

// SMPPClientInfo contains information about a connected SMPP client.
type SMPPClientInfo struct {
	Username  string    `json:"username"`
	IPAddress string    `json:"ip_address"`
	LastSeen  time.Time `json:"last_seen"`
}

// MM4ClientInfo contains information about a connected MM4 client.
type MM4ClientInfo struct {
	ClientID       string    `json:"client_id"`
	Username       string    `json:"username"`
	ActiveSessions int       `json:"active_sessions"`
	FirstConnectAt time.Time `json:"first_connect_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

// SetupStatsRoutes sets up the HTTP routes for statistics.
func SetupStatsRoutes(app *iris.Application, gateway *Gateway) {
	stats := app.Party("/stats", gateway.basicAuthMiddleware)
	{
		stats.Get("/", func(ctx iris.Context) {
			statsResponse := StatsResponse{}

			// Collect SMPP Server Stats
			gateway.SMPPServer.mu.RLock()
			smppConnCount := len(gateway.SMPPServer.conns)
			statsResponse.SMPPConnectedClients = smppConnCount

			smppClients := make([]SMPPClientInfo, 0, smppConnCount)
			for username, session := range gateway.SMPPServer.conns {
				ip, err := gateway.SMPPServer.GetClientIP(session)
				if err != nil {
					logrus.WithFields(logrus.Fields{
						"username": username,
						"error":    err,
					}).Error("Failed to get IP for SMPP session")
					ip = "unknown"
				}

				smppClients = append(smppClients, SMPPClientInfo{
					Username:  username,
					IPAddress: ip,
					LastSeen:  session.LastSeen,
				})
			}
			gateway.SMPPServer.mu.RUnlock()
			statsResponse.SMPPClients = smppClients

			// Collect MM4 Server Stats
			gateway.MM4Server.mu.RLock()
			mm4Clients := make([]MM4ClientInfo, 0)
			totalMM4Sessions := 0
			for clientID, state := range gateway.MM4Server.clientStates {
				sessionCount := state.SessionCount()
				totalMM4Sessions += sessionCount
				mm4Clients = append(mm4Clients, MM4ClientInfo{
					ClientID:       clientID,
					Username:       state.Username,
					ActiveSessions: sessionCount,
					FirstConnectAt: state.FirstConnectAt,
					LastActivityAt: state.LastActivityAt,
				})
			}
			gateway.MM4Server.mu.RUnlock()
			statsResponse.MM4ConnectedClients = totalMM4Sessions
			statsResponse.MM4Clients = mm4Clients

			// Return the stats as JSON
			ctx.JSON(statsResponse)
		})
	}
}

// basicAuthMiddleware is a middleware that enforces Basic Authentication using an API key
func (gateway *Gateway) basicAuthMiddleware(ctx iris.Context) {
	// Retrieve the expected API key from environment variables
	var lm = gateway.LogManager

	expectedAPIKey := os.Getenv("API_KEY")
	if expectedAPIKey == "" {
		lm.SendLog(lm.BuildLog(
			"Server.Web.AuthMiddleware",
			"Authenticated web client",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip": ctx.Values().GetString("client_ip"),
			},
		))
		// Log the error
		/*logf := LoggingFormat{
			Type:    "middleware_auth",
			Level:   logrus.ErrorLevel,
			message: "API_KEY environment variable not set",
		}
		logf.Print()*/

		// Respond with 500 Internal Server Error
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.WriteString("Internal Server Error")
		return
	}

	// Get the Authorization header
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		// Missing Authorization header
		unauthorized(ctx, gateway, "Authorization header missing")
		return
	}

	// Check if the Authorization header starts with "Basic "
	const prefix = "Basic "
	if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
		// Invalid Authorization header format
		unauthorized(ctx, gateway, "Invalid Authorization header format")
		return
	}

	// Decode the Base64 encoded credentials
	encodedCredentials := authHeader[len(prefix):]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		// Failed to decode credentials
		unauthorized(ctx, gateway, "Failed to decode credentials")
		return
	}
	credentials := string(decodedBytes)

	// In Basic Auth, credentials are in the format "username:password"
	colonIndex := indexOf(credentials, ':')
	if colonIndex < 0 {
		// Invalid credentials format
		unauthorized(ctx, gateway, "Invalid credentials format")
		return
	}

	// Extract the API key (password) from the credentials
	// Username can be ignored or used as needed
	// For this example, we'll assume the API key is the password
	apiKey := credentials[colonIndex+1:]

	// Compare the provided API key with the expected one
	if apiKey != expectedAPIKey {
		// Invalid API key
		unauthorized(ctx, gateway, "Invalid API key")
		return
	}

	// Authentication successful, proceed to the handler
	ctx.Next()
}

// SetupCarrierRoutes sets up the HTTP routes for carrier management
func SetupCarrierRoutes(app *iris.Application, gateway *Gateway) {
	carriers := app.Party("/carriers", gateway.basicAuthMiddleware)
	{
		// Add a new carrier
		carriers.Post("/", func(ctx iris.Context) {
			var carrier Carrier
			if err := ctx.ReadJSON(&carrier); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid carrier data"})
			}

			// Validate required fields
			if carrier.Name == "" || carrier.Type == "" || carrier.Username == "" || carrier.Password == "" {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "All fields (name, type, username, password) are required"})
			}

			if err := gateway.addCarrier(&carrier); err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": err.Error()})
			}

			// Return the carrier without exposing encrypted fields
			responseCarrier := Carrier{
				ID:   carrier.ID,
				Name: carrier.Name,
				Type: carrier.Type,
			}

			ctx.StatusCode(iris.StatusCreated)
			ctx.JSON(responseCarrier)
		})

		// Reload carriers from the database
		carriers.Post("/reload", func(ctx iris.Context) {
			if err := gateway.reloadCarriers(); err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": err.Error()})
			}

			ctx.JSON(iris.Map{"status": "Carriers reloaded"})
		})

		// Get all carriers
		carriers.Get("/", func(ctx iris.Context) {
			gateway.mu.RLock()
			defer gateway.mu.RUnlock()

			var carrierList []Carrier
			/*for _, handler := range gateway.Carriers {
				// Assuming each handler can provide its Carrier information
				if base, ok := handler.(*BaseCarrierHandler); ok {
					carrierList = append(carrierList, Carrier{Name: base.Name})
				}
			}*/

			ctx.JSON(carrierList)
		})
	}
}

// indexOf finds the index of the first occurrence of sep in s
func indexOf(s string, sep byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return i
		}
	}
	return -1
}

// unauthorized responds with a 401 status and a WWW-Authenticate header
func unauthorized(ctx iris.Context, gateway *Gateway, message string) {
	// Log the unauthorized access attempt
	var lm = gateway.LogManager
	lm.SendLog(lm.BuildLog(
		"Server.Web.AuthMiddleware",
		"Unauthorized web client",
		logrus.ErrorLevel,
		map[string]interface{}{
			"ip": ctx.Values().GetString("client_ip"),
		},
	))

	// Set the WWW-Authenticate header to indicate Basic Auth is required
	ctx.Header("WWW-Authenticate", `Basic realm="Restricted"`)

	// Respond with 401 Unauthorized
	ctx.StatusCode(http.StatusUnauthorized)
	ctx.WriteString(message)
}

// SetupClientRoutes sets up the HTTP routes for client management.
func SetupClientRoutes(app *iris.Application, gateway *Gateway) {
	clients := app.Party("/clients", gateway.basicAuthMiddleware)
	{
		// Add a new client
		clients.Post("/", func(ctx iris.Context) {
			var client Client
			if err := ctx.ReadJSON(&client); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client data"})
				return
			}

			// Validate required fields
			if client.Username == "" || client.Password == "" {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Username and Password are required"})
				return
			}

			// Legacy clients require an address (IP or hostname) for SMPP ACL and MM4 delivery
			if (client.Type == "" || client.Type == "legacy") && client.Address == "" {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Address (IP or hostname) is required for legacy clients"})
				return
			}

			if err := gateway.addClient(&client); err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": err.Error()})
				return
			}

			// Return the client without exposing encrypted fields
			responseClient := Client{
				ID:         client.ID,
				Username:   client.Username,
				Name:       client.Name,
				Type:       client.Type,
				Address:    client.Address,
				LogPrivacy: client.LogPrivacy,
			}

			ctx.StatusCode(iris.StatusCreated)
			ctx.JSON(responseClient)
		})

		// Reload clients and numbers from the database
		clients.Post("/reload", func(ctx iris.Context) {
			if err := gateway.reloadClientsAndNumbers(); err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": err.Error()})
				return
			}

			ctx.JSON(iris.Map{"status": "Clients and Numbers reloaded"})
		})

		// Update client password
		clients.Patch("/{id}/password", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID format"})
				return
			}

			var passwordUpdate PasswordUpdateRequest
			if err := ctx.ReadJSON(&passwordUpdate); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid request data"})
				return
			}

			// Validate the new password
			if passwordUpdate.NewPassword == "" {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "New password is required"})
				return
			}

			// Update the password
			if err := gateway.updateClientPassword(uint(clientID), passwordUpdate.NewPassword); err != nil {
				if err.Error() == "client not found in memory" {
					ctx.StatusCode(iris.StatusNotFound)
					ctx.JSON(iris.Map{"error": "Client not found"})
					return
				}
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": err.Error()})
				return
			}

			ctx.JSON(iris.Map{"status": "Password updated successfully"})
		})

		// Add a new number to a client
		clients.Post("/{id}/numbers", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			var newNumber ClientNumber
			if err := ctx.ReadJSON(&newNumber); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid number data"})
				return
			}

			// Validate required fields
			if newNumber.Number == "" || newNumber.Carrier == "" {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Number and Carrier are required"})
				return
			}

			// Add the number to the client
			if err := gateway.addNumber(uint(clientID), &newNumber); err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": err.Error()})
				return
			}

			// Return the newly added number
			responseNumber := ClientNumber{
				ID:       newNumber.ID,
				ClientID: newNumber.ClientID,
				Number:   newNumber.Number,
				Carrier:  newNumber.Carrier,
			}

			ctx.StatusCode(iris.StatusCreated)
			ctx.JSON(responseNumber)
		})

		// Get all numbers for a specific client
		clients.Get("/{id}/numbers", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			client := gateway.getClientByID(uint(clientID))
			if client == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			ctx.JSON(client.Numbers)
		})

		// Get ClientSettings for a client
		clients.Get("/{id}/settings", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			client := gateway.getClientByID(uint(clientID))
			if client == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			if client.Settings == nil {
				ctx.JSON(iris.Map{"message": "No web settings configured for this client"})
				return
			}

			ctx.JSON(client.Settings)
		})

		// Update ClientSettings for a client
		clients.Put("/{id}/settings", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			client := gateway.getClientByID(uint(clientID))
			if client == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			var updateReq struct {
				// Auth & Format
				AuthMethod *string `json:"auth_method,omitempty"`
				APIFormat  *string `json:"api_format,omitempty"`
				// Web-specific
				DisableMessageSplitting *bool   `json:"disable_message_splitting,omitempty"`
				WebhookRetries          *int    `json:"webhook_retries,omitempty"`
				WebhookTimeoutSecs      *int    `json:"webhook_timeout_secs,omitempty"`
				IncludeRawSegments      *bool   `json:"include_raw_segments,omitempty"`
				DefaultWebhook          *string `json:"default_webhook,omitempty"`
				// SMS Limits
				SMSBurstLimit   *int64 `json:"sms_burst_limit,omitempty"`
				SMSDailyLimit   *int64 `json:"sms_daily_limit,omitempty"`
				SMSMonthlyLimit *int64 `json:"sms_monthly_limit,omitempty"`
				// MMS Limits
				MMSBurstLimit   *int64 `json:"mms_burst_limit,omitempty"`
				MMSDailyLimit   *int64 `json:"mms_daily_limit,omitempty"`
				MMSMonthlyLimit *int64 `json:"mms_monthly_limit,omitempty"`
				// Limit Behavior
				LimitBoth *bool `json:"limit_both,omitempty"`
			}

			if err := ctx.ReadJSON(&updateReq); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid request body"})
				return
			}

			// Create settings if they don't exist
			if client.Settings == nil {
				client.Settings = &ClientSettings{ClientID: client.ID}
			}

			// Apply updates - Auth & Format
			if updateReq.AuthMethod != nil {
				client.Settings.AuthMethod = *updateReq.AuthMethod
			}
			if updateReq.APIFormat != nil {
				client.Settings.APIFormat = *updateReq.APIFormat
			}
			// Web-specific
			if updateReq.DisableMessageSplitting != nil {
				client.Settings.DisableMessageSplitting = *updateReq.DisableMessageSplitting
			}
			if updateReq.WebhookRetries != nil {
				client.Settings.WebhookRetries = *updateReq.WebhookRetries
			}
			if updateReq.WebhookTimeoutSecs != nil {
				client.Settings.WebhookTimeoutSecs = *updateReq.WebhookTimeoutSecs
			}
			if updateReq.IncludeRawSegments != nil {
				client.Settings.IncludeRawSegments = *updateReq.IncludeRawSegments
			}
			if updateReq.DefaultWebhook != nil {
				client.Settings.DefaultWebhook = *updateReq.DefaultWebhook
			}
			// SMS Limits
			if updateReq.SMSBurstLimit != nil {
				client.Settings.SMSBurstLimit = *updateReq.SMSBurstLimit
			}
			if updateReq.SMSDailyLimit != nil {
				client.Settings.SMSDailyLimit = *updateReq.SMSDailyLimit
			}
			if updateReq.SMSMonthlyLimit != nil {
				client.Settings.SMSMonthlyLimit = *updateReq.SMSMonthlyLimit
			}
			// MMS Limits
			if updateReq.MMSBurstLimit != nil {
				client.Settings.MMSBurstLimit = *updateReq.MMSBurstLimit
			}
			if updateReq.MMSDailyLimit != nil {
				client.Settings.MMSDailyLimit = *updateReq.MMSDailyLimit
			}
			if updateReq.MMSMonthlyLimit != nil {
				client.Settings.MMSMonthlyLimit = *updateReq.MMSMonthlyLimit
			}
			// Limit Behavior
			if updateReq.LimitBoth != nil {
				client.Settings.LimitBoth = *updateReq.LimitBoth
			}

			// Save to database
			if err := gateway.DB.Save(client.Settings).Error; err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to save settings"})
				return
			}

			ctx.JSON(iris.Map{
				"message":  "Settings updated",
				"settings": client.Settings,
			})
		})

		// Get all clients
		clients.Get("/", func(ctx iris.Context) {
			gateway.mu.RLock()
			defer gateway.mu.RUnlock()

			var clientList []Client
			for _, client := range gateway.Clients {
				// Return clients without exposing sensitive information
				c := Client{
					ID:         client.ID,
					Username:   client.Username,
					Name:       client.Name,
					Address:    client.Address,
					Type:       client.Type,
					Timezone:   client.Timezone,
					LogPrivacy: client.LogPrivacy,
					Settings:   client.Settings,
					Numbers:    client.Numbers,
				}
				clientList = append(clientList, c)
			}

			ctx.JSON(clientList)
		})

		// Update a number's properties
		clients.Put("/{id}/numbers/{number_id}", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			numberIDStr := ctx.Params().Get("number_id")
			numberID, err := strconv.ParseUint(numberIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid number ID"})
				return
			}

			client := gateway.getClientByID(uint(clientID))
			if client == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			// Find the number in the client's numbers
			var targetNumber *ClientNumber
			for i := range client.Numbers {
				if client.Numbers[i].ID == uint(numberID) {
					targetNumber = &client.Numbers[i]
					break
				}
			}
			if targetNumber == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Number not found for this client"})
				return
			}

			var updateReq struct {
				Carrier              *string `json:"carrier,omitempty"`
				Tag                  *string `json:"tag,omitempty"`
				Group                *string `json:"group,omitempty"`
				Webhook              *string `json:"webhook,omitempty"`
				IgnoreStopCmdSending *bool   `json:"ignore_stop_cmd_sending,omitempty"`
			}

			if err := ctx.ReadJSON(&updateReq); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid request body"})
				return
			}

			// Apply updates
			if updateReq.Carrier != nil {
				targetNumber.Carrier = *updateReq.Carrier
			}
			if updateReq.Tag != nil {
				targetNumber.Tag = *updateReq.Tag
			}
			if updateReq.Group != nil {
				targetNumber.Group = *updateReq.Group
			}
			if updateReq.Webhook != nil {
				targetNumber.WebHook = *updateReq.Webhook
			}
			if updateReq.IgnoreStopCmdSending != nil {
				targetNumber.IgnoreStopCmdSending = *updateReq.IgnoreStopCmdSending
			}

			// Save to database
			if err := gateway.DB.Save(targetNumber).Error; err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to update number"})
				return
			}

			ctx.JSON(iris.Map{
				"message": "Number updated",
				"number":  targetNumber,
			})
		})

		// Delete a number from a client
		clients.Delete("/{id}/numbers/{number_id}", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			numberIDStr := ctx.Params().Get("number_id")
			numberID, err := strconv.ParseUint(numberIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid number ID"})
				return
			}

			client := gateway.getClientByID(uint(clientID))
			if client == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			// Find the number in the client's numbers
			var targetNumber *ClientNumber
			var targetIndex int
			for i := range client.Numbers {
				if client.Numbers[i].ID == uint(numberID) {
					targetNumber = &client.Numbers[i]
					targetIndex = i
					break
				}
			}
			if targetNumber == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Number not found for this client"})
				return
			}

			// Delete NumberSettings if exists
			if targetNumber.Settings != nil {
				gateway.DB.Delete(targetNumber.Settings)
			}

			// Delete from database
			if err := gateway.DB.Delete(targetNumber).Error; err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to delete number"})
				return
			}

			// Remove from in-memory map
			gateway.mu.Lock()
			delete(gateway.Numbers, targetNumber.Number)
			// Remove from client's Numbers slice
			client.Numbers = append(client.Numbers[:targetIndex], client.Numbers[targetIndex+1:]...)
			gateway.mu.Unlock()

			ctx.JSON(iris.Map{"message": "Number deleted", "number_id": numberID})
		})

		// Delete a client
		clients.Delete("/{id}", func(ctx iris.Context) {
			clientIDStr := ctx.Params().Get("id")
			clientID, err := strconv.ParseUint(clientIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid client ID"})
				return
			}

			client := gateway.getClientByID(uint(clientID))
			if client == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			// Delete all NumberSettings for this client's numbers
			for _, num := range client.Numbers {
				if num.Settings != nil {
					gateway.DB.Delete(num.Settings)
				}
				gateway.DB.Delete(&num)
				gateway.mu.Lock()
				delete(gateway.Numbers, num.Number)
				gateway.mu.Unlock()
			}

			// Delete ClientSettings
			if client.Settings != nil {
				gateway.DB.Delete(client.Settings)
			}

			// Delete the client from database
			if err := gateway.DB.Delete(&Client{}, clientID).Error; err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to delete client"})
				return
			}

			// Remove from in-memory map
			gateway.mu.Lock()
			delete(gateway.Clients, client.Username)
			gateway.mu.Unlock()

			ctx.JSON(iris.Map{"message": "Client deleted", "client_id": clientID})
		})
	}
}

// SetupNumberRoutes sets up the HTTP routes for number management (standalone).
func SetupNumberRoutes(app *iris.Application, gateway *Gateway) {
	numbers := app.Party("/numbers", gateway.basicAuthMiddleware)
	{
		// Update NumberSettings for a number
		numbers.Put("/{id}/settings", func(ctx iris.Context) {
			numberIDStr := ctx.Params().Get("id")
			numberID, err := strconv.ParseUint(numberIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid number ID"})
				return
			}

			// Find the number in the gateway's numbers map
			var targetNumber *ClientNumber
			gateway.mu.RLock()
			for _, num := range gateway.Numbers {
				if num.ID == uint(numberID) {
					targetNumber = num
					break
				}
			}
			gateway.mu.RUnlock()

			if targetNumber == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Number not found"})
				return
			}

			var updateReq struct {
				// SMS Limits
				SMSBurstLimit   *int64 `json:"sms_burst_limit,omitempty"`
				SMSDailyLimit   *int64 `json:"sms_daily_limit,omitempty"`
				SMSMonthlyLimit *int64 `json:"sms_monthly_limit,omitempty"`
				// MMS Limits
				MMSBurstLimit   *int64 `json:"mms_burst_limit,omitempty"`
				MMSDailyLimit   *int64 `json:"mms_daily_limit,omitempty"`
				MMSMonthlyLimit *int64 `json:"mms_monthly_limit,omitempty"`
				// Limit Behavior
				LimitBoth *bool `json:"limit_both,omitempty"`
			}

			if err := ctx.ReadJSON(&updateReq); err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid request body"})
				return
			}

			// Create settings if they don't exist
			if targetNumber.Settings == nil {
				targetNumber.Settings = &NumberSettings{NumberID: targetNumber.ID}
			}

			// Apply updates
			if updateReq.SMSBurstLimit != nil {
				targetNumber.Settings.SMSBurstLimit = *updateReq.SMSBurstLimit
			}
			if updateReq.SMSDailyLimit != nil {
				targetNumber.Settings.SMSDailyLimit = *updateReq.SMSDailyLimit
			}
			if updateReq.SMSMonthlyLimit != nil {
				targetNumber.Settings.SMSMonthlyLimit = *updateReq.SMSMonthlyLimit
			}
			if updateReq.MMSBurstLimit != nil {
				targetNumber.Settings.MMSBurstLimit = *updateReq.MMSBurstLimit
			}
			if updateReq.MMSDailyLimit != nil {
				targetNumber.Settings.MMSDailyLimit = *updateReq.MMSDailyLimit
			}
			if updateReq.MMSMonthlyLimit != nil {
				targetNumber.Settings.MMSMonthlyLimit = *updateReq.MMSMonthlyLimit
			}
			if updateReq.LimitBoth != nil {
				targetNumber.Settings.LimitBoth = *updateReq.LimitBoth
			}

			// Save to database
			if err := gateway.DB.Save(targetNumber.Settings).Error; err != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(iris.Map{"error": "Failed to save settings"})
				return
			}

			// Also update the client's in-memory reference
			client := gateway.getClientByID(targetNumber.ClientID)
			if client != nil {
				for i := range client.Numbers {
					if client.Numbers[i].ID == targetNumber.ID {
						client.Numbers[i].Settings = targetNumber.Settings
						break
					}
				}
			}

			ctx.JSON(iris.Map{
				"message":  "Number settings updated",
				"settings": targetNumber.Settings,
			})
		})

		// Get NumberSettings for a number
		numbers.Get("/{id}/settings", func(ctx iris.Context) {
			numberIDStr := ctx.Params().Get("id")
			numberID, err := strconv.ParseUint(numberIDStr, 10, 32)
			if err != nil {
				ctx.StatusCode(iris.StatusBadRequest)
				ctx.JSON(iris.Map{"error": "Invalid number ID"})
				return
			}

			// Find the number
			var targetNumber *ClientNumber
			gateway.mu.RLock()
			for _, num := range gateway.Numbers {
				if num.ID == uint(numberID) {
					targetNumber = num
					break
				}
			}
			gateway.mu.RUnlock()

			if targetNumber == nil {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Number not found"})
				return
			}

			if targetNumber.Settings == nil {
				ctx.JSON(iris.Map{"message": "No settings configured for this number"})
				return
			}

			ctx.JSON(targetNumber.Settings)
		})
	}
}
func (gateway *Gateway) webInboundCarrier(ctx iris.Context) {
	// Extract the 'carrier' parameter from the URL
	carrier := ctx.Params().Get("carrier") // the carrier is the uuid of the carrier
	if carrier == "" {
		// Respond with 400 Bad Request
		ctx.StatusCode(http.StatusBadRequest)
		ctx.WriteString("carrier parameter is required")
		return
	}

	// Retrieve the corresponding inbound route handler
	carrierObj, exists := gateway.CarrierUUIDs[carrier]
	if exists {
		inboundRoute, exists := gateway.Carriers[carrierObj.Name]
		if exists {

			// Call the Inbound method of the carrier handler
			err := inboundRoute.Inbound(ctx)
			if err != nil {
				// Respond with 500 Internal Server Error
				ctx.StatusCode(http.StatusInternalServerError)
				ctx.WriteString("failed to process inbound message")
				return
			}
			return
		}
		// Successfully processed the inbound message
		return
	}

	// Log the error for unknown carrier
	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"WebServer.Carrier",
		"Unknown carrier UUID",
		logrus.WarnLevel,
		map[string]interface{}{
			"carrier_uuid": carrier,
		},
	))
	// Respond with 404 Not Found
	ctx.StatusCode(http.StatusNotFound)
	ctx.WriteString("carrier not found")
}

func (gateway *Gateway) webMediaFile(ctx iris.Context) {
	// Extract the access token from the URL
	accessToken := ctx.Params().Get("token")

	// Get client info for logging
	clientIP := ctx.Values().GetString("client_ip")
	if clientIP == "" {
		clientIP = ctx.RemoteAddr()
	}
	userAgent := ctx.GetHeader("User-Agent")

	if accessToken == "" {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"WebServer.Media.Access",
			"Missing access token",
			logrus.WarnLevel,
			map[string]interface{}{
				"client_ip":  clientIP,
				"user_agent": userAgent,
				"success":    false,
			},
		))
		ctx.StatusCode(http.StatusBadRequest)
		ctx.WriteString("access token is required")
		return
	}

	// Retrieve the media file from the database using UUID token
	mediaFile, err := gateway.getMediaFileByToken(accessToken)
	if err != nil {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"WebServer.Media.Access",
			"Failed to retrieve media file",
			logrus.WarnLevel,
			map[string]interface{}{
				"access_token": accessToken,
				"client_ip":    clientIP,
				"user_agent":   userAgent,
				"success":      false,
				"error_reason": err.Error(),
			},
		))
		ctx.StatusCode(http.StatusNotFound)
		ctx.WriteString("media file not found")
		return
	}

	if strings.Contains(mediaFile.ContentType, "application/smil") {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"WebServer.Media.Access",
			"SMIL content type not supported",
			logrus.WarnLevel,
			map[string]interface{}{
				"access_token": accessToken,
				"client_ip":    clientIP,
				"user_agent":   userAgent,
				"success":      false,
				"error_reason": "smil_not_supported",
			},
		))
		ctx.StatusCode(500)
		return
	}

	// Decode the Base64-encoded data
	fileBytes, err := base64.StdEncoding.DecodeString(mediaFile.Base64Data)
	if err != nil {
		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"WebServer.Media.Access",
			"Failed to decode Base64 media data",
			logrus.ErrorLevel,
			map[string]interface{}{
				"access_token": accessToken,
				"client_ip":    clientIP,
				"user_agent":   userAgent,
				"success":      false,
				"error_reason": "base64_decode_failed",
			},
		))
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.WriteString("failed to decode file data")
		return
	}

	// Log successful access
	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"WebServer.Media.Access",
		"Media file accessed",
		logrus.InfoLevel,
		map[string]interface{}{
			"access_token": accessToken,
			"file_name":    mediaFile.FileName,
			"content_type": mediaFile.ContentType,
			"client_ip":    clientIP,
			"user_agent":   userAgent,
			"success":      true,
		},
	))

	// Set the appropriate Content-Type header
	ctx.ContentType(mediaFile.ContentType)

	// Optionally, set Content-Disposition to suggest a filename for download
	// Uncomment the following line if you want the browser to prompt a download
	// ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", mediaFile.FileName))

	// Send the file bytes as the response
	ctx.Write(fileBytes)
}

func (gateway *Gateway) webReloadData(ctx iris.Context) {
	if err := gateway.reloadClientsAndNumbers(); err != nil {
		ctx.StatusCode(500)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"status": "Data reloaded successfully"})
}

func isPrivateIP(ip net.IP) bool {
	for _, cidr := range trustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func ProxyIPMiddleware(ctx iris.Context) {
	remoteIP := net.ParseIP(ctx.RemoteAddr())
	if remoteIP == nil {
		ctx.Values().Set("client_ip", ctx.RemoteAddr())
		ctx.Next()
		return
	}

	if !isPrivateIP(remoteIP) {
		ctx.Values().Set("client_ip", remoteIP.String())
		ctx.Next()
		return
	}

	if forwardedFor := ctx.GetHeader("X-Forwarded-For"); forwardedFor != "" {
		ips := strings.Split(forwardedFor, ",")
		for _, ip := range ips {
			parsedIP := net.ParseIP(strings.TrimSpace(ip))
			if parsedIP != nil && !isPrivateIP(parsedIP) {
				ctx.Values().Set("client_ip", parsedIP.String())
				ctx.Next()
				return
			}
		}
	}

	if realIP := ctx.GetHeader("X-Real-IP"); realIP != "" {
		parsedIP := net.ParseIP(realIP)
		if parsedIP != nil && !isPrivateIP(parsedIP) {
			ctx.Values().Set("client_ip", parsedIP.String())
			ctx.Next()
			return
		}
	}

	// If we couldn't determine a public IP, fall back to the remote address
	ctx.Values().Set("client_ip", ctx.RemoteAddr())
	ctx.Next()
}

// clientAuthMiddleware authenticates using Basic Auth (username:password) or Bearer token.
// Bearer token should be the base64-encoded username:password (for Bicom compatibility).
// Only 'web' type clients can use this API - legacy clients must use SMPP.
// Auth method is validated against client's api_format setting:
// - bicom: expects Bearer token
// - generic/telnyx/other: expects Basic Auth
func (gateway *Gateway) clientAuthMiddleware(ctx iris.Context) {
	var username, password string
	var authMethod string // "basic" or "bearer"

	// Try Basic Auth first
	username, password, ok := ctx.Request().BasicAuth()
	if ok {
		authMethod = "basic"
	}

	// If no Basic Auth, try Bearer token
	if !ok {
		authHeader := ctx.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			// Token is base64(username:password)
			decoded, err := base64.StdEncoding.DecodeString(token)
			if err == nil {
				parts := strings.SplitN(string(decoded), ":", 2)
				if len(parts) == 2 {
					username = parts[0]
					password = parts[1]
					authMethod = "bearer"
					ok = true
				}
			}
		}
	}

	if !ok || username == "" {
		unauthorized(ctx, gateway, "Authentication required")
		return
	}

	gateway.mu.RLock()
	client, exists := gateway.Clients[username]
	gateway.mu.RUnlock()

	if !exists {
		unauthorized(ctx, gateway, "Invalid credentials")
		return
	}

	// Password check
	if client.Password != password {
		unauthorized(ctx, gateway, "Invalid credentials")
		return
	}

	// Only web clients can use REST API - legacy clients must use SMPP
	if client.Type != "" && client.Type != "web" {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(iris.Map{
			"status":  "error",
			"message": "Legacy clients must use SMPP protocol, not REST API",
		})
		return
	}

	// Validate auth method matches client's auth_method setting
	expectedAuth := "basic" // default
	if client.Settings != nil && client.Settings.AuthMethod != "" {
		expectedAuth = client.Settings.AuthMethod
	}

	if expectedAuth != authMethod {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(iris.Map{
			"status":  "error",
			"message": fmt.Sprintf("Client requires %s authentication", expectedAuth),
		})
		return
	}

	// Set client in context
	ctx.Values().Set("client", client)
	ctx.Values().Set("auth_method", authMethod)
	ctx.Next()
}

// WebMediaItem represents a media file in the JSON payload.
type WebMediaItem struct {
	Filename    string `json:"filename"`     // e.g. "photo.jpg"
	Content     string `json:"content"`      // Base64 encoded content
	ContentType string `json:"content_type"` // e.g. "image/jpeg"
	URL         string `json:"url"`          // Alternative: URL to media (for Bicom/Telnyx)
}

// WebMessageRequest represents the incoming JSON payload for sending a message (generic format).
type WebMessageRequest struct {
	ClientID uint           `json:"client_id"` // Explicit client ID (validated against auth)
	From     string         `json:"from"`      // Sender number (must belong to client)
	To       string         `json:"to"`
	Text     string         `json:"text"`
	Media    []WebMediaItem `json:"media,omitempty"`
}

// BicomMessageRequest represents Bicom's inbound message format.
type BicomMessageRequest struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Text      string   `json:"text"`
	MediaURLs []string `json:"media_urls,omitempty"`
}

// TelnyxMessageRequest represents Telnyx's webhook-like inbound format.
type TelnyxMessageRequest struct {
	Data struct {
		EventType string `json:"event_type"`
		Payload   struct {
			From struct {
				PhoneNumber string `json:"phone_number"`
			} `json:"from"`
			To []struct {
				PhoneNumber string `json:"phone_number"`
			} `json:"to"`
			Text  string `json:"text"`
			Media []struct {
				URL         string `json:"url"`
				ContentType string `json:"content_type"`
			} `json:"media,omitempty"`
		} `json:"payload"`
	} `json:"data"`
}

// ParsedMessage is the normalized internal format after parsing any API format.
type ParsedMessage struct {
	From  string
	To    string
	Text  string
	Media []WebMediaItem
}

// fetchMediaFromURL downloads media content from a URL and returns the content bytes, content type, and filename.
// This is used when web clients (Bicom/Telnyx) send media_urls instead of base64 content.
func fetchMediaFromURL(mediaURL string, timeout time.Duration) (content []byte, contentType string, filename string, err error) {
	client := &http.Client{
		Timeout: timeout,
	}

	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("non-OK HTTP status: %s", resp.Status)
	}

	content, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", fmt.Errorf("error reading media content: %w", err)
	}

	// Get content type from response header
	contentType = resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Derive filename from URL path
	filename = path.Base(mediaURL)
	if filename == "" || filename == "." || filename == "/" {
		filename = uuid.New().String()
	}

	return content, contentType, filename, nil
}

// SetupMessageRoutes sets up the HTTP routes for message sending (Web Clients).
func SetupMessageRoutes(app *iris.Application, gateway *Gateway) {
	messages := app.Party("/messages", gateway.clientAuthMiddleware)
	{
		// Handler for sending messages - shared between /messages/send and POST /messages
		sendMessageHandler := func(ctx iris.Context) {
			lm := gateway.LogManager

			// Log incoming request immediately (before any processing)
			clientIP := ctx.RemoteAddr()
			authHeader := ctx.GetHeader("Authorization")
			authType := "none"
			if strings.HasPrefix(authHeader, "Bearer ") {
				authType = "bearer"
			} else if strings.HasPrefix(authHeader, "Basic ") {
				authType = "basic"
			}
			lm.SendLog(lm.BuildLog(
				"WebServer.Messages.Send",
				"IncomingRequest",
				logrus.InfoLevel,
				map[string]interface{}{
					"clientIP":    clientIP,
					"authType":    authType,
					"userAgent":   ctx.GetHeader("User-Agent"),
					"contentType": ctx.GetHeader("Content-Type"),
				},
			))

			// Get authenticated client
			client := ctx.Values().Get("client").(*Client)

			// Determine API format from client settings
			apiFormat := "generic"
			if client.Settings != nil && client.Settings.APIFormat != "" {
				apiFormat = client.Settings.APIFormat
			}

			lm.SendLog(lm.BuildLog(
				"WebServer.Messages.Send",
				"ClientAuthenticated",
				logrus.InfoLevel,
				map[string]interface{}{
					"clientID":   client.ID,
					"clientName": client.Username,
					"apiFormat":  apiFormat,
					"clientType": client.Type,
				},
			))

			// Parse request based on format
			var parsed ParsedMessage
			switch apiFormat {
			case "bicom":
				var req BicomMessageRequest
				if err := ctx.ReadJSON(&req); err != nil {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"status": "error", "message": "Invalid Bicom format request body"})
					return
				}
				parsed.From = req.From
				parsed.To = req.To
				parsed.Text = req.Text
				// Convert media URLs to WebMediaItems
				for _, url := range req.MediaURLs {
					parsed.Media = append(parsed.Media, WebMediaItem{URL: url})
				}

			case "telnyx":
				var req TelnyxMessageRequest
				if err := ctx.ReadJSON(&req); err != nil {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "Invalid Telnyx format request body"})
					return
				}
				parsed.From = req.Data.Payload.From.PhoneNumber
				if len(req.Data.Payload.To) > 0 {
					parsed.To = req.Data.Payload.To[0].PhoneNumber
				}
				parsed.Text = req.Data.Payload.Text
				for _, m := range req.Data.Payload.Media {
					parsed.Media = append(parsed.Media, WebMediaItem{URL: m.URL, ContentType: m.ContentType})
				}

			default: // "generic"
				var req WebMessageRequest
				if err := ctx.ReadJSON(&req); err != nil {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "Invalid request body"})
					return
				}
				// Validate client_id matches authenticated client
				if req.ClientID != 0 && req.ClientID != client.ID {
					ctx.StatusCode(iris.StatusForbidden)
					ctx.JSON(iris.Map{"error": "Authenticated client does not match specified client_id"})
					return
				}
				parsed.From = req.From
				parsed.To = req.To
				parsed.Text = req.Text
				parsed.Media = req.Media
			}

			// Log parsed message details (including media URLs for debugging)
			mediaURLs := make([]string, 0, len(parsed.Media))
			for _, m := range parsed.Media {
				if m.URL != "" {
					mediaURLs = append(mediaURLs, m.URL)
				}
			}
			textPreview := parsed.Text
			if len(textPreview) > 50 {
				textPreview = textPreview[:50] + "..."
			}
			lm.SendLog(lm.BuildLog(
				"WebServer.Messages.Send",
				"MessageParsed",
				logrus.InfoLevel,
				map[string]interface{}{
					"clientID":    client.ID,
					"clientName":  client.Username,
					"apiFormat":   apiFormat,
					"from":        parsed.From,
					"to":          parsed.To,
					"textPreview": textPreview,
					"textLength":  len(parsed.Text),
					"mediaCount":  len(parsed.Media),
					"mediaURLs":   mediaURLs,
				},
			))

			if parsed.To == "" {
				if apiFormat == "bicom" {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"status": "error", "message": "'to' field is required"})
				} else {
					ctx.StatusCode(iris.StatusBadRequest)
					ctx.JSON(iris.Map{"error": "'to' field is required"})
				}
				return
			}

			// Validate From
			fromNumber := parsed.From
			if fromNumber == "" {
				if len(client.Numbers) == 1 {
					fromNumber = client.Numbers[0].Number
				} else {
					if apiFormat == "bicom" {
						ctx.StatusCode(iris.StatusBadRequest)
						ctx.JSON(iris.Map{"status": "error", "message": "'from' field is required when multiple numbers exist"})
					} else {
						ctx.StatusCode(iris.StatusBadRequest)
						ctx.JSON(iris.Map{"error": "'from' field is required when multiple numbers exist"})
					}
					return
				}
			} else {
				// verify ownership
				owned := false
				for _, n := range client.Numbers {
					if n.Number == fromNumber { // loose match? or exact?
						owned = true
						break
					}
					// Check e164 match if stored with +
					if strings.TrimPrefix(n.Number, "+") == strings.TrimPrefix(fromNumber, "+") {
						fromNumber = n.Number // Use stored format
						owned = true
						break
					}
				}
				if !owned {
					if apiFormat == "bicom" {
						ctx.StatusCode(iris.StatusForbidden)
						ctx.JSON(iris.Map{"status": "error", "message": "You do not own the 'from' number"})
					} else {
						ctx.StatusCode(iris.StatusForbidden)
						ctx.JSON(iris.Map{"error": "You do not own the 'from' number"})
					}
					return
				}
			}

			// Construct Queue Item
			msgType := MsgQueueItemType.SMS
			if len(parsed.Media) > 0 {
				msgType = MsgQueueItemType.MMS
			}

			// --- COMPREHENSIVE LIMIT CHECK (Synchronous for API) ---
			// Use the same limit checking logic as the router for consistency
			limitResult := gateway.CheckMessageLimits(client, fromNumber, string(msgType), "outbound")
			if limitResult != nil && !limitResult.Allowed {
				if apiFormat == "bicom" {
					ctx.StatusCode(iris.StatusTooManyRequests)
					ctx.JSON(iris.Map{"status": "error", "message": limitResult.Message})
				} else {
					ctx.StatusCode(iris.StatusTooManyRequests)
					ctx.JSON(iris.Map{
						"error":         "rate_limit_exceeded",
						"message":       limitResult.Message,
						"limit_type":    limitResult.LimitType,
						"period":        limitResult.Period,
						"number":        limitResult.Number,
						"current_usage": limitResult.CurrentUsage,
						"limit":         limitResult.Limit,
					})
				}
				return
			}
			// --- END LIMIT CHECK ---

			// Parse Media - fetch from URLs if needed and prepare for transcoding
			var files []MsgFile
			var originalSizeBytes int
			if msgType == MsgQueueItemType.MMS {
				for _, media := range parsed.Media {
					var content []byte
					var contentType string
					var filename string

					if media.URL != "" {
						// Fetch media from URL (for Bicom/Telnyx clients)
						lm.SendLog(lm.BuildLog(
							"WebServer.Messages.Send",
							"FetchingMediaFromURL",
							logrus.InfoLevel,
							map[string]interface{}{
								"clientID":   client.ID,
								"clientName": client.Username,
								"url":        media.URL,
							},
						))

						fetchedContent, fetchedContentType, fetchedFilename, err := fetchMediaFromURL(media.URL, 30*time.Second)
						if err != nil {
							lm.SendLog(lm.BuildLog(
								"WebServer.Messages.Send",
								"MediaFetchError",
								logrus.ErrorLevel,
								map[string]interface{}{
									"clientID":   client.ID,
									"clientName": client.Username,
									"url":        media.URL,
									"error":      err.Error(),
								},
							))
							// Return error to client for Bicom format
							if apiFormat == "bicom" {
								ctx.StatusCode(iris.StatusBadRequest)
								ctx.JSON(iris.Map{"status": "error", "message": fmt.Sprintf("Failed to fetch media from URL: %s", media.URL)})
							} else {
								ctx.StatusCode(iris.StatusBadRequest)
								ctx.JSON(iris.Map{"error": fmt.Sprintf("Failed to fetch media from URL: %s", media.URL)})
							}
							return
						}

						content = fetchedContent
						contentType = fetchedContentType
						filename = fetchedFilename

						// Use provided content type if available
						if media.ContentType != "" {
							contentType = media.ContentType
						}
						// Use provided filename if available
						if media.Filename != "" {
							filename = media.Filename
						}

						lm.SendLog(lm.BuildLog(
							"WebServer.Messages.Send",
							"MediaFetchSuccess",
							logrus.InfoLevel,
							map[string]interface{}{
								"clientID":    client.ID,
								"clientName":  client.Username,
								"url":         media.URL,
								"contentType": contentType,
								"filename":    filename,
								"sizeBytes":   len(content),
							},
						))
					} else if media.Content != "" {
						// Decode base64 content (for generic format with inline content)
						decodedContent, err := base64.StdEncoding.DecodeString(media.Content)
						if err != nil {
							lm.SendLog(lm.BuildLog(
								"WebServer.Messages.Send",
								"Base64DecodeError",
								logrus.ErrorLevel,
								map[string]interface{}{
									"clientID":   client.ID,
									"clientName": client.Username,
									"filename":   media.Filename,
									"error":      err.Error(),
								},
							))
							ctx.StatusCode(iris.StatusBadRequest)
							ctx.JSON(iris.Map{"error": "Invalid base64 content in media"})
							return
						}
						content = decodedContent
						contentType = media.ContentType
						filename = media.Filename
					} else {
						// No content or URL provided
						lm.SendLog(lm.BuildLog(
							"WebServer.Messages.Send",
							"MediaNoContentOrURL",
							logrus.WarnLevel,
							map[string]interface{}{
								"clientID":   client.ID,
								"clientName": client.Username,
							},
						))
						continue
					}

					originalSizeBytes += len(content)
					files = append(files, MsgFile{
						Filename:    filename,
						ContentType: contentType,
						Content:     content, // Raw bytes for transcoding
					})
				}
			}

			logID := uuid.New().String()

			// Get client IP for tracking
			clientIP = ctx.Values().GetString("client_ip")
			if clientIP == "" {
				clientIP = ctx.RemoteAddr()
			}

			// For MMS with files, route through transcoding pipeline (like MM4)
			if msgType == MsgQueueItemType.MMS && len(files) > 0 {
				// Create MM4Message for transcoding (similar to MM4 inbound flow)
				mm4Message := &MM4Message{
					From:          fromNumber,
					To:            parsed.To,
					Files:         files,
					TransactionID: logID,
					MessageID:     logID,
					Client:        client,
				}

				lm.SendLog(lm.BuildLog(
					"WebServer.Messages.Send",
					"RoutingToTranscoder",
					logrus.InfoLevel,
					map[string]interface{}{
						"logID":             logID,
						"clientID":          client.ID,
						"clientName":        client.Username,
						"from":              fromNumber,
						"to":                parsed.To,
						"fileCount":         len(files),
						"originalSizeBytes": originalSizeBytes,
					},
				))

				// Send to transcoding channel (async processing)
				gateway.MM4Server.MediaTranscodeChan <- mm4Message
			} else {
				// SMS or MMS without media - route directly
				item := MsgQueueItem{
					LogID:             logID,
					To:                parsed.To,
					From:              fromNumber,
					Type:              msgType,
					message:           parsed.Text,
					files:             files,
					ReceivedTimestamp: time.Now(),
					SourceIP:          clientIP,
					OriginalSizeBytes: originalSizeBytes,
				}

				// Inject into Router
				gateway.Router.ClientMsgChan <- item
			}

			// Return success immediately (Async)
			// Bicom expects HTTP 200 with {"status": "success", "message": ""}
			if apiFormat == "bicom" {
				ctx.StatusCode(iris.StatusOK)
				ctx.JSON(iris.Map{"status": "success", "message": ""})
			} else {
				ctx.StatusCode(iris.StatusAccepted)
				ctx.JSON(iris.Map{
					"status": "queued",
					"id":     logID,
				})
			}
		}

		// Register both routes - /messages/send (original) and /messages (Bicom compatibility)
		messages.Post("/send", sendMessageHandler)
		messages.Post("/", sendMessageHandler) // Bicom sends to /messages instead of /messages/send

		// GET /messages/usage - Check current usage against limits
		messages.Get("/usage", func(ctx iris.Context) {
			client := ctx.Values().Get("client").(*Client)

			// Helper to build period usage
			buildPeriodUsage := func(msgType string, period string) iris.Map {
				periodStart := GetPeriodStart(client, period)
				used, _ := gateway.GetUsageCountByType(client.ID, "", msgType, periodStart)

				var limit int64
				if client.Settings != nil {
					limit, _, _ = getEffectiveLimit(client.Settings, nil, msgType, period)
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

			// Build comprehensive client usage
			clientUsage := iris.Map{
				"username": client.Username,
				"type":     client.Type,
				"sms": iris.Map{
					"burst":   buildPeriodUsage("sms", "burst"),
					"daily":   buildPeriodUsage("sms", "daily"),
					"monthly": buildPeriodUsage("sms", "monthly"),
				},
				"mms": iris.Map{
					"burst":   buildPeriodUsage("mms", "burst"),
					"daily":   buildPeriodUsage("mms", "daily"),
					"monthly": buildPeriodUsage("mms", "monthly"),
				},
			}

			// Build per-number usage (focusing on daily for numbers)
			numberUsage := make([]iris.Map, 0, len(client.Numbers))
			dailyStart := GetPeriodStart(client, "daily")

			for _, num := range client.Numbers {
				// Use direction-aware counting to match limit enforcement behavior (outbound only by default)
				smsUsedOut, _ := gateway.GetUsageCountWithDirection(client.ID, num.Number, "sms", "outbound", dailyStart)
				mmsUsedOut, _ := gateway.GetUsageCountWithDirection(client.ID, num.Number, "mms", "outbound", dailyStart)

				// Get number limits and limit_both setting
				var smsDailyLimit, mmsDailyLimit int64
				limitBoth := false
				if num.Settings != nil {
					if num.Settings.SMSDailyLimit > 0 {
						smsDailyLimit = num.Settings.SMSDailyLimit
					}
					if num.Settings.MMSDailyLimit > 0 {
						mmsDailyLimit = num.Settings.MMSDailyLimit
					}
					limitBoth = num.Settings.LimitBoth
				}

				entry := iris.Map{
					"number":    num.Number,
					"direction": "outbound",
					"sms": iris.Map{
						"current_usage": smsUsedOut,
						"limit":         smsDailyLimit,
					},
					"mms": iris.Map{
						"current_usage": mmsUsedOut,
						"limit":         mmsDailyLimit,
					},
					"limit_both": limitBoth,
				}
				if num.Tag != "" {
					entry["tag"] = num.Tag
				}
				if num.Group != "" {
					entry["group"] = num.Group
				}
				numberUsage = append(numberUsage, entry)
			}

			// Calculate reset times using client's timezone
			burstReset := time.Now().Add(time.Minute)
			dailyReset := GetPeriodStart(client, "daily").Add(24 * time.Hour)
			monthlyReset := GetPeriodStart(client, "monthly").AddDate(0, 1, 0)

			ctx.JSON(iris.Map{
				"client":   clientUsage,
				"numbers":  numberUsage,
				"timezone": client.Timezone,
				"reset_times": iris.Map{
					"burst":   burstReset.Format(time.RFC3339),
					"daily":   dailyReset.Format(time.RFC3339),
					"monthly": monthlyReset.Format(time.RFC3339),
				},
				"timestamp": time.Now().Format(time.RFC3339),
			})
		})
	}
}

package main

import (
	"encoding/base64"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

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
	ClientID    string    `json:"client_id"`
	ConnectedAt time.Time `json:"connected_at"`
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
			mm4ConnCount := len(gateway.MM4Server.connectedClients)
			statsResponse.MM4ConnectedClients = mm4ConnCount

			mm4Clients := make([]MM4ClientInfo, 0, mm4ConnCount)
			for clientID, connectedAt := range gateway.MM4Server.connectedClients {
				mm4Clients = append(mm4Clients, MM4ClientInfo{
					ClientID:    clientID,
					ConnectedAt: connectedAt,
				})
			}
			gateway.MM4Server.mu.RUnlock()
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
			Message: "API_KEY environment variable not set",
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

		// Add a new number to a client
		clients.Post("/{id}/numbers", func(ctx iris.Context) {
			clientID := ctx.Params().Get("id")
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
			if err := gateway.addNumber(clientID, &newNumber); err != nil {
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
			clientID := ctx.Params().Get("id")

			gateway.mu.RLock()
			client, exists := gateway.Clients[clientID]
			gateway.mu.RUnlock()

			if !exists {
				ctx.StatusCode(iris.StatusNotFound)
				ctx.JSON(iris.Map{"error": "Client not found"})
				return
			}

			ctx.JSON(client.Numbers)
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
					LogPrivacy: client.LogPrivacy,
					Numbers:    client.Numbers,
				}
				clientList = append(clientList, c)
			}

			ctx.JSON(clientList)
		})
	}
}
func (gateway *Gateway) webInboundCarrier(ctx iris.Context) {
	// Extract the 'carrier' parameter from the URL
	carrier := ctx.Params().Get("carrier")
	if carrier == "" {
		// Log the error
		/*logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", "carrier parameter is missing")
		logf.Level = logrus.ErrorLevel
		logf.Message = "Missing carrier parameter in request"
		logf.Print()*/

		// Respond with 400 Bad Request
		ctx.StatusCode(http.StatusBadRequest)
		ctx.WriteString("carrier parameter is required")
		return
	}

	// Retrieve the corresponding inbound route handler
	inboundRoute, exists := gateway.Carriers[carrier]
	if exists {
		// Call the Inbound method of the carrier handler
		err := inboundRoute.Inbound(ctx)
		if err != nil {
			// Log the error
			/*logf := LoggingFormat{
				Type: LogType.Carrier + "_" + LogType.Inbound,
			}
			logf.AddField("carrier", carrier)
			logf.AddField("error", err.Error())
			logf.Level = logrus.ErrorLevel
			logf.Message = "Failed to process inbound message"
			logf.Print()*/

			// Respond with 500 Internal Server Error
			ctx.StatusCode(http.StatusInternalServerError)
			ctx.WriteString("failed to process inbound message")
			return
		}
		// Successfully processed the inbound message
		return
	}

	// Log the error for unknown carrier
	/*logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Inbound,
	}
	logf.AddField("carrier", carrier)
	logf.Level = logrus.WarnLevel
	logf.Message = "Unknown carrier"
	logf.Print()
	*/
	// Respond with 404 Not Found
	ctx.StatusCode(http.StatusNotFound)
	ctx.WriteString("carrier not found")
}

func (gateway *Gateway) webMediaFile(ctx iris.Context) {
	// Extract the 'id' parameter from the URL
	fileID := ctx.Params().Get("id")
	id, err := strconv.ParseInt(fileID, 10, 64)
	if fileID == "" || err != nil {
		// Log the error
		/*logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", "file ID is required")
		logf.Level = logrus.ErrorLevel
		logf.Message = "Missing file ID in request"
		logf.Print()*/

		// Respond with 400 Bad Request
		ctx.StatusCode(http.StatusBadRequest)
		ctx.WriteString("file ID is required")
		return
	}

	// Retrieve the media file from MongoDB
	mediaFile, err := gateway.getMediaFile(uint(id))
	if err != nil {
		// Log the error
		/*logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}*/
		/*logf.AddField("error", err.Error())
		logf.AddField("fileID", fileID)
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to retrieve media file from MongoDB"
		logf.Print()*/

		// Respond with 404 Not Found
		ctx.StatusCode(http.StatusNotFound)
		ctx.WriteString("media file not found")
		return
	}

	if strings.Contains(mediaFile.ContentType, "application/smil") {
		// todo
		ctx.StatusCode(500)
		return
	}

	// Decode the Base64-encoded data
	fileBytes, err := base64.StdEncoding.DecodeString(mediaFile.Base64Data)
	if err != nil {
		// Log the error
		/*logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", err.Error())
		logf.AddField("fileID", fileID)
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to decode Base64 media data"
		logf.Print()*/

		// Respond with 500 Internal Server Error
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.WriteString("failed to decode file data")
		return
	}

	// Set the appropriate Msg-Type header
	ctx.ContentType(mediaFile.ContentType)

	// Optionally, set Msg-Disposition to suggest a filename for download
	// Uncomment the following line if you want the browser to prompt a download
	// ctx.Header("Msg-Disposition", fmt.Sprintf("attachment; filename=%s", mediaFile.FileName))

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

var trustedProxies = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"fc00::/7",
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

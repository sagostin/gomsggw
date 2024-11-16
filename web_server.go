package main

import (
	"encoding/base64"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
)

// basicAuthMiddleware is a middleware that enforces Basic Authentication using an API key
func basicAuthMiddleware(ctx iris.Context) {
	// Retrieve the expected API key from environment variables
	expectedAPIKey := os.Getenv("API_KEY")
	if expectedAPIKey == "" {
		// Log the error
		logf := LoggingFormat{
			Type:    "middleware_auth",
			Level:   logrus.ErrorLevel,
			Message: "API_KEY environment variable not set",
		}
		logf.Print()

		// Respond with 500 Internal Server Error
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.WriteString("Internal Server Error")
		return
	}

	// Get the Authorization header
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		// Missing Authorization header
		unauthorized(ctx, "Authorization header missing")
		return
	}

	// Check if the Authorization header starts with "Basic "
	const prefix = "Basic "
	if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
		// Invalid Authorization header format
		unauthorized(ctx, "Invalid Authorization header format")
		return
	}

	// Decode the Base64 encoded credentials
	encodedCredentials := authHeader[len(prefix):]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		// Failed to decode credentials
		unauthorized(ctx, "Failed to decode credentials")
		return
	}
	credentials := string(decodedBytes)

	// In Basic Auth, credentials are in the format "username:password"
	colonIndex := indexOf(credentials, ':')
	if colonIndex < 0 {
		// Invalid credentials format
		unauthorized(ctx, "Invalid credentials format")
		return
	}

	// Extract the API key (password) from the credentials
	// Username can be ignored or used as needed
	// For this example, we'll assume the API key is the password
	apiKey := credentials[colonIndex+1:]

	// Compare the provided API key with the expected one
	if apiKey != expectedAPIKey {
		// Invalid API key
		unauthorized(ctx, "Invalid API key")
		return
	}

	// Authentication successful, proceed to the handler
	ctx.Next()
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
func unauthorized(ctx iris.Context, message string) {
	// Log the unauthorized access attempt
	logf := LoggingFormat{
		Type:    "middleware_auth",
		Level:   logrus.WarnLevel,
		Message: message,
	}
	logf.AddField("client_ip", ctx.RemoteAddr())
	logf.Print()

	// Set the WWW-Authenticate header to indicate Basic Auth is required
	ctx.Header("WWW-Authenticate", `Basic realm="Restricted"`)

	// Respond with 401 Unauthorized
	ctx.StatusCode(http.StatusUnauthorized)
	ctx.WriteString("Unauthorized")
}

func (gateway *Gateway) webInboundCarrier(ctx iris.Context) {
	// Extract the 'carrier' parameter from the URL
	carrier := ctx.Params().Get("carrier")
	if carrier == "" {
		// Log the error
		logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", "carrier parameter is missing")
		logf.Level = logrus.ErrorLevel
		logf.Message = "Missing carrier parameter in request"
		logf.Print()

		// Respond with 400 Bad Request
		ctx.StatusCode(http.StatusBadRequest)
		ctx.WriteString("carrier parameter is required")
		return
	}

	// Retrieve the corresponding inbound route handler
	inboundRoute, exists := gateway.Carriers[carrier]
	if exists {
		// Call the Inbound method of the carrier handler
		err := inboundRoute.Inbound(ctx, gateway)
		if err != nil {
			// Log the error
			logf := LoggingFormat{
				Type: LogType.Carrier + "_" + LogType.Inbound,
			}
			logf.AddField("carrier", carrier)
			logf.AddField("error", err.Error())
			logf.Level = logrus.ErrorLevel
			logf.Message = "Failed to process inbound message"
			logf.Print()

			// Respond with 500 Internal Server Error
			ctx.StatusCode(http.StatusInternalServerError)
			ctx.WriteString("failed to process inbound message")
			return
		}
		// Successfully processed the inbound message
		return
	}

	// Log the error for unknown carrier
	logf := LoggingFormat{
		Type: LogType.Carrier + "_" + LogType.Inbound,
	}
	logf.AddField("carrier", carrier)
	logf.Level = logrus.WarnLevel
	logf.Message = "Unknown carrier"
	logf.Print()

	// Respond with 404 Not Found
	ctx.StatusCode(http.StatusNotFound)
	ctx.WriteString("carrier not found")
}

func (gateway *Gateway) webMediaFile(ctx iris.Context) {
	// Extract the 'id' parameter from the URL
	fileID := ctx.Params().Get("id")
	if fileID == "" {
		// Log the error
		logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", "file ID is required")
		logf.Level = logrus.ErrorLevel
		logf.Message = "Missing file ID in request"
		logf.Print()

		// Respond with 400 Bad Request
		ctx.StatusCode(http.StatusBadRequest)
		ctx.WriteString("file ID is required")
		return
	}

	// Retrieve the media file from MongoDB
	mediaFile, err := getMediaFromMongoDB(gateway.MongoClient, fileID)
	if err != nil {
		// Log the error
		logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", err.Error())
		logf.AddField("fileID", fileID)
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to retrieve media file from MongoDB"
		logf.Print()

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
		logf := LoggingFormat{
			Type: LogType.Carrier + "_" + LogType.Inbound,
		}
		logf.AddField("error", err.Error())
		logf.AddField("fileID", fileID)
		logf.Level = logrus.ErrorLevel
		logf.Message = "Failed to decode Base64 media data"
		logf.Print()

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

func (gateway *Gateway) webReloadClients(ctx iris.Context) {
	// Log the successful access
	logf := LoggingFormat{
		Type:    "reload_clients",
		Level:   logrus.InfoLevel,
		Message: "Reload connectedClients route accessed successfully",
	}
	logf.AddField("client_ip", ctx.RemoteAddr())
	logf.Print()

	clients, err := loadClients()
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "failed to load clients"
	}

	clientMap := make(map[string]*Client)
	for i := range clients {
		clientMap[clients[i].Username] = &clients[i]
	}

	gateway.Clients = clientMap
	//gateway.SMPPServer.connectedClients = clientMap

	// Respond with 200 OK and a message
	ctx.StatusCode(http.StatusOK)
	ctx.WriteString("Access Granted: 200 OK")
}

func webHealthCheck(ctx iris.Context) {
	// Log the successful access
	logf := LoggingFormat{
		Type:    "protected_route",
		Level:   logrus.InfoLevel,
		Message: "Protected route accessed successfully",
	}
	logf.AddField("client_ip", ctx.RemoteAddr())
	logf.Print()

	// Respond with 200 OK and a message
	ctx.StatusCode(http.StatusOK)
	ctx.WriteString("Access Granted: 200 OK")
}

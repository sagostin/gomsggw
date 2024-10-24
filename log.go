package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strconv"
	"time"
)

type LoggingFormat struct {
	Message        string                 `json:"message,omitempty"`
	Error          error                  `json:"error,omitempty"`
	Type           string                 `json:"type,omitempty"`
	Level          logrus.Level           `json:"level,omitempty"`
	AdditionalData map[string]interface{} `json:"additional_data,omitempty"`
}

func (e *LoggingFormat) AddField(key string, value interface{}) {
	if e.AdditionalData == nil {
		e.AdditionalData = make(map[string]interface{})
	}
	e.AdditionalData[key] = value
}

// LogTypeStruct represents specific types of log messages within a category.
type LogTypeStruct struct {
	// Carrier-related log types
	// prefixes
	Carrier  string
	Endpoint string
	Server   string
	MM4      string
	SMPP     string
	// status - second prefix
	Startup  string
	Shutdown string
	// suffixes
	Routing        string
	Queue          string
	Inbound        string
	Outbound       string
	Authentication string
	// Add more log types as needed
	DEBUG string
}

// LogType is a singleton instance of LogTypeStruct containing all log types.
var LogType = LogTypeStruct{
	// Initialize the log types with lowercase and underscores
	//directions - prefix
	Carrier:  "carrier",
	Endpoint: "endpoint",
	Server:   "server",
	// service types - prefix
	MM4:  "mm4",
	SMPP: "smpp",
	// statuses - second prefix
	Startup:  "startup",
	Shutdown: "shutdown",
	// direction / type - suffix
	Inbound:        "inbound",
	Outbound:       "outbound",
	Routing:        "routing",
	Queue:          "queue",
	Authentication: "authentication",
	DEBUG:          "debug",
}

// LogMessages contains standardized log message templates.
var LogMessages = struct {
	Transaction    string // used for sending/receiving mms/sms transactions
	Authentication string
	// Add more messages as needed
}{
	// Initialize the message templates
	Transaction:    "%s, %s - from: %s, to: %s",      // direction, carrier - from: number, to: number
	Authentication: "%s, auth: %s, user: %s, ip: %s", // server (mm4/smpp), success/failed (err), userid, ip
}

func (e *LoggingFormat) String() string {
	marshal, err := json.Marshal(e)
	if err != nil {
		return ""
	}

	return string(marshal)
}

func (e *LoggingFormat) ToError() error {
	e.Print()
	// todo send logs over to loki??!??
	return fmt.Errorf(e.String())
}

func (e *LoggingFormat) Print() {
	// todo send over to loki as well??
	debugEnabled := os.Getenv("DEBUG") == "true"
	if debugEnabled {
		switch e.Level.String() {
		case "warning":
			logrus.Warn(e.String())
		case "error":
			logrus.Error(e.String())
		case "debug":
			logrus.Debug(e.String())
		default:
			logrus.Info(e.String())
		}
	}

	lokiClient := NewLokiClient(os.Getenv("LOKI_URL"), os.Getenv("LOKI_USERNAME"), os.Getenv("LOKI_PASSWORD"))
	labels := map[string]string{"job": "sms-mms-gateway", "server_id": os.Getenv("SERVER_ID")}

	data, err := json.Marshal(e)
	if err != nil {
		logrus.Error("error marshalling to send to loki")
		return
	}
	err = lokiClient.PushLog(labels, LogEntry{Timestamp: time.Now(), Line: string(data)})
	if err != nil {
		logrus.Error("error sending to loki")
		logrus.Error(err)
		return
	}
}

// LokiClient holds the configuration for the Loki client.
type LokiClient struct {
	PushURL  string // URL to Loki's push API
	Username string // Username for basic auth
	Password string // Password for basic auth
}

// LogEntry struct to use time.Time for Timestamp
type LogEntry struct {
	Timestamp time.Time
	Line      string
}

// LokiPushData represents the data structure required by Loki's push API.
type LokiPushData struct {
	Streams []LokiStream `json:"streams"`
}

// LokiStream represents a stream of logs with the same labels in Loki.
type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // Array of [timestamp, line] tuples
}

// NewLokiClient creates a new client to interact with Loki.
func NewLokiClient(pushURL, username, password string) *LokiClient {
	return &LokiClient{
		PushURL:  pushURL,
		Username: username,
		Password: password,
	}
}

// PushLog sends a log entry to Loki.
func (c *LokiClient) PushLog(labels map[string]string, entry LogEntry) error {
	// Convert time to Unix epoch in nanoseconds
	unixNano := entry.Timestamp.UnixNano()
	timestampStr := strconv.FormatInt(unixNano, 10)

	/*// Ensure the log line is properly escaped for JSON
	escapedLine := strconv.Quote(entry.Line)
	// Remove the surrounding quotes added by Quote
	escapedLine = escapedLine[1 : len(escapedLine)-1]*/

	payload := LokiPushData{
		Streams: []LokiStream{
			{
				Stream: labels,
				Values: [][2]string{{timestampStr, entry.Line}},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling json: %w", err)
	}

	req, err := http.NewRequest("POST", c.PushURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to Loki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received unexpected response status: %d", resp.StatusCode)
	}

	return nil
}

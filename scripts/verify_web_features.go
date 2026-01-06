package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Config
const (
	BaseURL  = "http://localhost:3000"
	Username = "API_USER_TEST" // Needs to be seeded or existing
	Password = "API_PASSWORD_TEST"
)

func main() {
	println("NOTE: This script assumes the server is running and a client 'API_USER_TEST' exists with password 'API_PASSWORD_TEST'.")
	println("If not, please update credentials in script or seed DB.")

	// Test 1: Send Message (Inbound API)
	println("\n--- Test 1: POST /messages/send ---")
	payload := map[string]interface{}{
		"to":   "+19998887777", // A carrier number
		"text": "Hello from Web Client Verification Script!",
		//"from": "+15551234567", // If not present, should use default if single number
	}

	status, body, err := post("/messages/send", payload)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Status: %d\nBody: %s\n", status, body)
	}

	// Wait for async processing
	time.Sleep(2 * time.Second)
	println("Did check server logs for 'RoutingDecision' and 'Limit' checks.")
}

func post(path string, data interface{}) (int, string, error) {
	jsonBytes, _ := json.Marshal(data)
	req, _ := http.NewRequest("POST", BaseURL+path, bytes.NewBuffer(jsonBytes))
	req.SetBasicAuth(Username, Password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

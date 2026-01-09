package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type Route struct {
	Type     string
	Endpoint string
	Handler  CarrierHandler
}

type Router struct {
	gateway          *Gateway
	Routes           []*Route
	ClientMsgChan    chan MsgQueueItem
	CarrierMsgChan   chan MsgQueueItem
	MessageAckStatus chan MsgQueueItem
}

// UnifiedRouter listens on both client and carrier channels and processes messages.
func (router *Router) UnifiedRouter() {
	for {
		select {
		case msg := <-router.ClientMsgChan:
			// "client" origin
			go router.processMessage(&msg, "client")
		case msg := <-router.CarrierMsgChan:
			// "carrier" origin
			go router.processMessage(&msg, "carrier")
		}
	}
}

// processMessage handles a message from either channel.
func (router *Router) processMessage(m *MsgQueueItem, origin string) {
	lm := router.gateway.LogManager

	// Format numbers
	to, _ := FormatToE164(m.To)
	m.To = to
	from, _ := FormatToE164(m.From)
	m.From = from

	// Compute convoID for queue management
	convoID := computeCorrelationKey(m.From, m.To)
	processingSuccessful := false

	// Ensure we release the queue if processing fails (only for client-origin messages)
	if origin == "client" {
		defer func() {
			if !processingSuccessful {
				router.gateway.ConvoManager.HandleFailure(convoID, router)
			}
		}()
	}

	// Lookup clients
	toClient, toClientErr := router.findClientByNumber(m.To)
	fromClient, fromClientErr := router.findClientByNumber(m.From)

	// Debug: Log routing decision info
	toClientUsername := ""
	if toClient != nil {
		toClientUsername = toClient.Username
	}
	fromClientUsername := ""
	if fromClient != nil {
		fromClientUsername = fromClient.Username
	}
	lm.SendLog(lm.BuildLog(
		"Router.DEBUG",
		"ProcessMessageEntry",
		logrus.DebugLevel,
		map[string]interface{}{
			"logID":              m.LogID,
			"origin":             origin,
			"msgType":            m.Type,
			"from":               m.From,
			"to":                 m.To,
			"toClientFound":      toClient != nil,
			"toClientUsername":   toClientUsername,
			"toClientErr":        fmt.Sprintf("%v", toClientErr),
			"fromClientFound":    fromClient != nil,
			"fromClientUsername": fromClientUsername,
			"fromClientErr":      fmt.Sprintf("%v", fromClientErr),
		},
	))

	if origin == "client" && fromClient == nil {
		lm.SendLog(lm.BuildLog("Router", "Invalid sender number", logrus.ErrorLevel, map[string]interface{}{
			"logID": m.LogID,
			"from":  m.From,
		}))
		return
	}
	// If both client lookups fail, log and return.
	// If both client lookups fail, log and return.
	if origin == "carrier" && toClient == nil {
		lm.SendLog(lm.BuildLog("Router", "Invalid destination number", logrus.ErrorLevel, map[string]interface{}{
			"logID": m.LogID,
			"from":  m.From,
		}))
		return
	}

	// --- COMPREHENSIVE LIMIT CHECK ---
	if fromClient != nil {
		// Determine message type for limit checking
		msgType := string(m.Type)

		// Use comprehensive limit checker (checks burst, daily, monthly for SMS/MMS with direction awareness)
		limitResult := router.gateway.CheckMessageLimits(fromClient, m.From, msgType, "outbound")
		if limitResult != nil && !limitResult.Allowed {
			lm.SendLog(lm.BuildLog("Router", "Message limit exceeded", logrus.WarnLevel, map[string]interface{}{
				"logID":     m.LogID,
				"client":    fromClient.Username,
				"from":      m.From,
				"limitType": limitResult.LimitType,
				"limit":     limitResult.Limit,
				"used":      limitResult.CurrentUsage,
				"period":    limitResult.Period,
				"msgType":   msgType,
			}))
			// Drop the message
			return
		}
	}
	// --- END LIMIT CHECK ---

	// Retry on the channel where the message came from
	retryChan := router.ClientMsgChan
	if origin == "carrier" {
		retryChan = router.CarrierMsgChan
	}

	// Process based on message type
	switch m.Type {
	case MsgQueueItemType.SMS:
		// Calculate SMS encoding, segment count, and byte length for records
		smsEncoding := GetSMSEncoding(m.message)
		smsSegments := GetSMSSegmentCount(m.message)
		smsBytesLength := len([]byte(m.message))

		// Debug: Log which path we're taking
		routePath := "CARRIER"
		if toClient != nil {
			if toClient.Type == "web" {
				routePath = "WEB_CLIENT"
			} else {
				routePath = "SMPP_CLIENT"
			}
		}
		lm.SendLog(lm.BuildLog(
			"Router.DEBUG.SMS",
			"RoutingDecision",
			logrus.DebugLevel,
			map[string]interface{}{
				"logID":     m.LogID,
				"from":      m.From,
				"to":        m.To,
				"routePath": routePath,
				"encoding":  smsEncoding,
				"segments":  smsSegments,
			},
		))

		if toClient != nil {
			// Check if destination is a WEB Client
			if toClient.Type == "web" {
				// WEB CLIENT DELIVERY LOGIC
				// Find valid webhook - first check number-specific, then fall back to client default
				webhookURL := ""
				for _, n := range toClient.Numbers {
					if strings.Contains(m.To, n.Number) {
						webhookURL = n.WebHook
						break
					}
				}

				// Fall back to client default webhook if number doesn't have one
				if webhookURL == "" && toClient.Settings != nil && toClient.Settings.DefaultWebhook != "" {
					webhookURL = toClient.Settings.DefaultWebhook
				}

				if webhookURL == "" {
					lm.SendLog(lm.BuildLog("Router.SMS", "No webhook defined for web client number or default", logrus.ErrorLevel, map[string]interface{}{
						"toClient": toClient.Username,
						"to":       m.To,
						"logID":    m.LogID,
					}))
					return
				}

				// Dispatch!
				if err := router.DispatchWebhook(webhookURL, m, toClient, fromClient); err != nil {
					lm.SendLog(lm.BuildLog("Router.SMS", "Failed to dispatch webhook", logrus.ErrorLevel, map[string]interface{}{
						"toClient": toClient.Username,
						"url":      webhookURL,
						"logID":    m.LogID,
					}, err))
					// Retry logic?
					if m.Retry("failed to dispatch webhook", retryChan) {
					}
					return
				}

				// Success Log
				lm.SendLog(lm.BuildLog(
					"Router.DEBUG.SMS",
					"WebhookSentSuccess",
					logrus.InfoLevel,
					map[string]interface{}{
						"logID":    m.LogID,
						"toClient": toClient.Username,
						"url":      webhookURL,
					},
				))

				internal := (fromClient != nil && toClient != nil)
				fromClientType := "carrier"
				carrierName := m.SourceCarrier // Source carrier for inbound from carrier
				if fromClient != nil {
					fromClientType = fromClient.Type
					carrierName = "" // No carrier for client-to-client
					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem:        *m,
						Carrier:             "", // Client-to-client, no carrier
						ClientID:            fromClient.ID,
						Internal:            internal,
						Direction:           "outbound",
						FromClientType:      fromClientType,
						ToClientType:        "web",
						DeliveryMethod:      "webhook",
						Encoding:            smsEncoding,
						TotalSegments:       smsSegments,
						OriginalBytesLength: smsBytesLength,
						SourceIP:            m.SourceIP,
					}
				}
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem:        *m,
					Carrier:             carrierName, // Source carrier if from carrier, empty if from client
					ClientID:            toClient.ID,
					Internal:            internal,
					Direction:           "inbound",
					FromClientType:      fromClientType,
					ToClientType:        "web",
					DeliveryMethod:      "webhook",
					Encoding:            smsEncoding,
					TotalSegments:       smsSegments,
					OriginalBytesLength: smsBytesLength,
					SourceIP:            m.SourceIP,
				}
				return
			}

			// ... Legacy SMPP Handling (Existing Code) ...
			// Get session directly by client username (more efficient than searching by number)
			session, err := router.gateway.SMPPServer.getSessionByUsername(toClient.Username)

			// Debug: Log session lookup result
			sessionFound := session != nil
			lm.SendLog(lm.BuildLog(
				"Router.DEBUG.SMS",
				"SMPPSessionLookup",
				logrus.DebugLevel,
				map[string]interface{}{
					"logID":        m.LogID,
					"to":           m.To,
					"toClient":     toClient.Username,
					"sessionFound": sessionFound,
					"lookupErr":    fmt.Sprintf("%v", err),
				},
			))

			if err != nil || session == nil {

				lm.SendLog(lm.BuildLog("Router.SMS", "Failed to find SMPP session", logrus.ErrorLevel, map[string]interface{}{
					"toClient": toClient.Username,
					"logID":    m.LogID,
				}, err))
				if m.Retry("failed to find SMPP session", retryChan) {
					// todo send error message back to sender if it is a found client as the sender
				}
				return
			}

			// Debug: Log before sending SMPP
			lm.SendLog(lm.BuildLog(
				"Router.DEBUG.SMS",
				"SendingSMPP",
				logrus.DebugLevel,
				map[string]interface{}{
					"logID":    m.LogID,
					"from":     m.From,
					"to":       m.To,
					"toClient": toClient.Username,
				},
			))

			if err := router.gateway.SMPPServer.sendSMPP(*m, session); err != nil {

				lm.SendLog(lm.BuildLog("Router.SMS", "Failed to send via SMPP", logrus.ErrorLevel, map[string]interface{}{
					"toClient": toClient.Username,
					"logID":    m.LogID,
					"msg":      m,
				}, err))
				if m.Retry("failed to send SMPP", retryChan) {
					// todo send error message back to sender if it is a found client as the sender
				}
				return
			}

			// Debug: Log successful SMPP send
			lm.SendLog(lm.BuildLog(
				"Router.DEBUG.SMS",
				"SMPPSentSuccess",
				logrus.InfoLevel,
				map[string]interface{}{
					"logID":    m.LogID,
					"from":     m.From,
					"to":       m.To,
					"toClient": toClient.Username,
					"internal": fromClient != nil && toClient != nil,
				},
			))

			// Record the successful send
			internal := (fromClient != nil && toClient != nil)
			fromClientType := "carrier"
			carrierName := m.SourceCarrier // Source carrier for inbound from carrier
			if fromClient != nil {
				fromClientType = fromClient.Type
				carrierName = "" // No carrier for client-to-client
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem:        *m,
					Carrier:             "",
					ClientID:            fromClient.ID,
					Internal:            internal,
					Direction:           "outbound",
					FromClientType:      fromClientType,
					ToClientType:        "legacy",
					DeliveryMethod:      "smpp",
					Encoding:            smsEncoding,
					TotalSegments:       smsSegments,
					OriginalBytesLength: smsBytesLength,
					SourceIP:            m.SourceIP,
				}
			}
			if toClient != nil {
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem:        *m,
					Carrier:             carrierName,
					ClientID:            toClient.ID,
					Internal:            internal,
					Direction:           "inbound",
					FromClientType:      fromClientType,
					ToClientType:        "legacy",
					DeliveryMethod:      "smpp",
					Encoding:            smsEncoding,
					TotalSegments:       smsSegments,
					OriginalBytesLength: smsBytesLength,
					SourceIP:            m.SourceIP,
				}
			}
		} else {
			carrier, _ := router.gateway.getClientCarrier(m.From)
			if carrier != "" {
				// add to outbound carrier queue
				route := router.gateway.Router.findRouteByName("carrier", carrier)
				if route != nil {
					ackID, err := route.Handler.SendSMS(m)
					if err != nil {

						if ackID == "STOP_MESSAGE" {
							msg := &MsgQueueItem{
								To:              m.From,
								From:            m.To,
								Type:            "sms",
								message:         "Blocked due to STOP message. Please try again later or contact our support if the issue persists. ID: " + m.LogID,
								SkipNumberCheck: false,
								LogID:           m.LogID,
								Delivery: &MsgQueueDelivery{
									Error:      "discard after first attempt",
									RetryTime:  time.Now(),
									RetryCount: 666,
								},
							}

							router.CarrierMsgChan <- *msg
							return
						} else {
							lm.SendLog(lm.BuildLog(
								"ROUTER.SMS",
								"RouterSendCarrier",
								logrus.ErrorLevel,
								map[string]interface{}{
									"client": safeClientUsername(fromClient),
									"logID":  m.LogID,
								}, err,
							))
							if m.Retry("failed to send SMPP to carrier", retryChan) {
								// todo send error message back to sender if it is a found client as the sender
								msg := &MsgQueueItem{
									To:              m.From,
									From:            m.To,
									Type:            "sms",
									message:         "An error occurred. Please try again later or contact our support if the issue persists. ID: " + m.LogID,
									SkipNumberCheck: false,
									LogID:           m.LogID,
									Delivery: &MsgQueueDelivery{
										Error:      "discard after first attempt",
										RetryTime:  time.Now(),
										RetryCount: 666,
									},
								}

								router.CarrierMsgChan <- *msg
							}
						}
						return
					}

					lm.SendLog(lm.BuildLog(
						"Router.SMS",
						"Successfully sent SMS",
						logrus.InfoLevel,
						map[string]interface{}{
							"logID":     m.LogID,
							"carrierID": ackID,
							"from":      m.From,
							"to":        m.To,
						}, nil,
					))

					// Compute the conversation hash.
					convoID = computeCorrelationKey(m.From, m.To)
					// Update the conversation queue with the expected ack.
					router.gateway.ConvoManager.SetExpectedAck(convoID, ackID, router, 10*time.Second)

					// We've successfully handed off to the carrier and are waiting for an async ACK.
					// Do NOT release the queue yet.
					processingSuccessful = true

					if fromClient != nil {
						router.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem:        *m,
							Carrier:             carrier,
							ClientID:            fromClient.ID,
							Internal:            false,
							Direction:           "outbound",
							FromClientType:      fromClient.Type,
							ToClientType:        "carrier",
							DeliveryMethod:      "carrier_api",
							Encoding:            smsEncoding,
							TotalSegments:       smsSegments,
							OriginalBytesLength: smsBytesLength,
							SourceIP:            m.SourceIP,
						}
					}
					/*if m.Delivery != nil {
						err = m.Delivery.Ack(false)
					}*/
					return
				}
			}
		}
	case MsgQueueItemType.MMS:
		if toClient != nil {
			// Check if destination is a WEB Client - use webhook delivery
			if toClient.Type == "web" {
				// WEB CLIENT MMS DELIVERY LOGIC
				// First check number-specific webhook, then fall back to client default
				webhookURL := ""
				for _, n := range toClient.Numbers {
					if strings.Contains(m.To, n.Number) {
						webhookURL = n.WebHook
						break
					}
				}

				// Fall back to client default webhook if number doesn't have one
				if webhookURL == "" && toClient.Settings != nil && toClient.Settings.DefaultWebhook != "" {
					webhookURL = toClient.Settings.DefaultWebhook
				}

				if webhookURL == "" {
					lm.SendLog(lm.BuildLog("Router.MMS", "No webhook defined for web client number or default", logrus.ErrorLevel, map[string]interface{}{
						"toClient": toClient.Username,
						"to":       m.To,
						"logID":    m.LogID,
					}))
					return
				}

				// Dispatch MMS via webhook
				if err := router.DispatchWebhook(webhookURL, m, toClient, fromClient); err != nil {
					lm.SendLog(lm.BuildLog("Router.MMS", "Failed to dispatch webhook", logrus.ErrorLevel, map[string]interface{}{
						"toClient": toClient.Username,
						"url":      webhookURL,
						"logID":    m.LogID,
					}, err))
					if m.Retry("failed to dispatch MMS webhook", retryChan) {
					}
					return
				}

				// Success Log
				lm.SendLog(lm.BuildLog(
					"Router.DEBUG.MMS",
					"WebhookSentSuccess",
					logrus.InfoLevel,
					map[string]interface{}{
						"logID":      m.LogID,
						"toClient":   toClient.Username,
						"url":        webhookURL,
						"mediaCount": len(m.files),
					},
				))

				internal := (fromClient != nil && toClient != nil)
				fromClientType := "carrier"
				carrierName := m.SourceCarrier // Source carrier for inbound from carrier
				if fromClient != nil {
					fromClientType = fromClient.Type
					carrierName = "" // No carrier for client-to-client
					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem:      *m,
						Carrier:           "",
						ClientID:          fromClient.ID,
						Internal:          internal,
						Direction:         "outbound",
						FromClientType:    fromClientType,
						ToClientType:      "web",
						DeliveryMethod:    "webhook",
						MediaCount:        len(m.files),
						OriginalSizeBytes: m.OriginalSizeBytes,
						SourceIP:          m.SourceIP,
					}
				}
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem:      *m,
					Carrier:           carrierName,
					ClientID:          toClient.ID,
					Internal:          internal,
					Direction:         "inbound",
					FromClientType:    fromClientType,
					ToClientType:      "web",
					DeliveryMethod:    "webhook",
					MediaCount:        len(m.files),
					OriginalSizeBytes: m.OriginalSizeBytes,
					SourceIP:          m.SourceIP,
				}
				return
			}

			// Legacy MM4 Client delivery
			if err := router.gateway.MM4Server.sendMM4(*m); err != nil {
				lm.SendLog(lm.BuildLog("Router", "Failed to send MM4: %s", logrus.ErrorLevel, map[string]interface{}{
					"toClient": toClient.Username,
					"logID":    m.LogID,
				}, err))
				if m.Retry("failed to send MM4", retryChan) {
					// todo send error message back to sender if it is a found client as the sender
				}
				return
			}
			internal := (fromClient != nil && toClient != nil)
			fromClientType := "carrier"
			carrierName := m.SourceCarrier // Source carrier for inbound from carrier
			if fromClient != nil {
				fromClientType = fromClient.Type
				carrierName = "" // No carrier for client-to-client
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem:      *m,
					Carrier:           "",
					ClientID:          fromClient.ID,
					Internal:          internal,
					Direction:         "outbound",
					FromClientType:    fromClientType,
					ToClientType:      "legacy",
					DeliveryMethod:    "mm4",
					MediaCount:        len(m.files),
					OriginalSizeBytes: m.OriginalSizeBytes,
					SourceIP:          m.SourceIP,
				}
			}
			if toClient != nil {
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem:      *m,
					Carrier:           carrierName,
					ClientID:          toClient.ID,
					Internal:          internal,
					Direction:         "inbound",
					FromClientType:    fromClientType,
					ToClientType:      "legacy",
					DeliveryMethod:    "mm4",
					MediaCount:        len(m.files),
					OriginalSizeBytes: m.OriginalSizeBytes,
					SourceIP:          m.SourceIP,
				}
			}
		} else {
			// For MMS, if no client is found, try routing via carrier
			carrier, _ := router.gateway.getClientCarrier(m.From)
			if carrier != "" {
				// add to outbound carrier queue
				route := router.gateway.Router.findRouteByName("carrier", carrier)
				if route != nil {
					ackID, err := route.Handler.SendMMS(m)
					if err != nil {

						if ackID == "STOP_MESSAGE" {
							msg := &MsgQueueItem{
								To:              m.From,
								From:            m.To,
								Type:            "mms",
								message:         "Blocked due to STOP message. Please try again later or contact our support if the issue persists. ID: " + m.LogID,
								SkipNumberCheck: false,
								LogID:           m.LogID,
								Delivery: &MsgQueueDelivery{
									Error:      "discard after first attempt",
									RetryTime:  time.Now(),
									RetryCount: 666,
								},
							}

							router.CarrierMsgChan <- *msg
							return
						} else {
							lm.SendLog(lm.BuildLog(
								"ROUTER.MMS",
								"RouterSendCarrier",
								logrus.ErrorLevel,
								map[string]interface{}{
									"client": safeClientUsername(fromClient),
									"logID":  m.LogID,
								}, err,
							))

							if m.Retry("failed to send MMS to carrier", retryChan) {
								msg := &MsgQueueItem{
									To:              m.From,
									From:            m.To,
									Type:            "mms",
									message:         "An error occurred. Please try again later or contact our support if the issue persists. ID: " + m.LogID,
									SkipNumberCheck: false,
									LogID:           m.LogID,
									Delivery: &MsgQueueDelivery{
										Error:      "discard after first attempt",
										RetryTime:  time.Now(),
										RetryCount: 666,
									},
								}

								router.CarrierMsgChan <- *msg
							}

							return
						}
					}

					lm.SendLog(lm.BuildLog(
						"Router.MMS",
						"Successfully sent MMS",
						logrus.InfoLevel,
						map[string]interface{}{
							"logID":     m.LogID,
							"carrierID": ackID,
							"from":      m.From,
							"to":        m.To,
						}, nil,
					))

					if fromClient != nil {
						router.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem:      *m,
							Carrier:           carrier,
							ClientID:          fromClient.ID,
							Internal:          false,
							Direction:         "outbound",
							FromClientType:    fromClient.Type,
							ToClientType:      "carrier",
							DeliveryMethod:    "carrier_api",
							MediaCount:        len(m.files),
							OriginalSizeBytes: m.OriginalSizeBytes,
							SourceIP:          m.SourceIP,
						}
					}
					return

				}
			} else {
				lm.SendLog(lm.BuildLog(
					"ROUTER.MMS",
					"RouterFindCarrier",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": safeClientUsername(fromClient),
						"logID":  m.LogID,
					},
				))
			}

			// throw error?
			lm.SendLog(lm.BuildLog(
				"ROUTER.MMS",
				"RouterSendFailed",
				logrus.ErrorLevel,
				map[string]interface{}{
					"client": safeClientUsername(fromClient),
					"logID":  m.LogID,
				},
			))
			return
		}
	}
}

func (router *Router) AddRoute(routeType, endpoint string, handler CarrierHandler) {
	router.Routes = append(router.Routes, &Route{Type: routeType, Endpoint: endpoint, Handler: handler})
}

func (router *Router) findRouteByName(routeType, routeName string) *Route {
	for _, route := range router.Routes {
		if route.Type == routeType && route.Endpoint == routeName {
			return route
		}
	}
	return nil
}

// findClientByNumber searches for a client using an E.164 number.
// The client's number list does not have the `+` prefix.
func (router *Router) findClientByNumber(number string) (*Client, error) {
	// Normalize the input number by removing the leading `+`, if present
	searchNumber := strings.TrimPrefix(number, "+")

	for _, client := range router.gateway.Clients {
		for _, num := range client.Numbers {
			// Compare the normalized input number with the stored number
			if strings.Contains(searchNumber, num.Number) {
				return client, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to find client for number: %s", number)
}

func FormatToE164(number string) (string, error) {
	// Preserve the original number
	originalNumber := number

	// Remove any metadata like "/TYPE=PLMN"
	number = strings.Split(number, "/")[0]

	// Regex to match a valid E.164 number
	e164Regex := regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)

	// Remove any non-digit characters except the leading '+'
	cleaned := strings.TrimLeft(number, "+")
	cleaned = regexp.MustCompile(`\D`).ReplaceAllString(cleaned, "")

	// Re-add the leading '+' if it was stripped
	if strings.HasPrefix(number, "+") {
		cleaned = "+" + cleaned
	} else {
		cleaned = "+" + cleaned // Add '+' if not already present
	}

	// Validate the cleaned number against E.164 format
	if !e164Regex.MatchString(cleaned) {
		return originalNumber, fmt.Errorf("unable to format to E.164: %s", originalNumber)
	}

	return cleaned, nil
}

// DispatchWebhook sends the message payload to a web client's webhook URL.
// Supports different API formats based on client's WebSettings.APIFormat:
// - 'generic' (default): Standard format
// - 'bicom': Bicom PBXware format with Bearer auth and media_urls
// - 'telnyx': Telnyx-style format
func (router *Router) DispatchWebhook(webhookURL string, item *MsgQueueItem, toClient *Client, fromClient *Client) error {
	// Determine API format
	apiFormat := "generic"
	if toClient != nil && toClient.Settings != nil && toClient.Settings.APIFormat != "" {
		apiFormat = toClient.Settings.APIFormat
	}

	// Build payload based on format
	var payload map[string]interface{}

	switch apiFormat {
	case "bicom":
		// Bicom format: { from, to, text, media_urls }
		payload = map[string]interface{}{
			"from": item.From,
			"to":   item.To,
			"text": item.message,
		}
		if item.Type == MsgQueueItemType.MMS && len(item.files) > 0 {
			// Bicom expects media_urls - we'll need to provide URLs or embed
			// For now, provide as base64 data URLs since we don't have hosted URLs
			mediaUrls := make([]string, 0, len(item.files))
			for _, f := range item.files {
				if len(f.Base64Data) > 0 {
					dataURL := fmt.Sprintf("data:%s;base64,%s", f.ContentType, f.Base64Data)
					mediaUrls = append(mediaUrls, dataURL)
				}
			}
			if len(mediaUrls) > 0 {
				payload["media_urls"] = mediaUrls
			}
		}

	case "telnyx":
		// Telnyx-style format
		payload = map[string]interface{}{
			"data": map[string]interface{}{
				"event_type": "message.received",
				"payload": map[string]interface{}{
					"id":          item.LogID,
					"from":        map[string]string{"phone_number": item.From},
					"to":          []map[string]string{{"phone_number": item.To}},
					"text":        item.message,
					"type":        string(item.Type),
					"received_at": item.ReceivedTimestamp,
				},
			},
		}

	default: // "generic"
		payload = map[string]interface{}{
			"id":        item.LogID,
			"from":      item.From,
			"to":        item.To,
			"text":      item.message,
			"timestamp": item.ReceivedTimestamp,
			"type":      item.Type,
		}

		// Add media if MMS
		if item.Type == MsgQueueItemType.MMS && len(item.files) > 0 {
			mediaList := make([]map[string]string, 0, len(item.files))
			for _, f := range item.files {
				m := map[string]string{
					"filename":     f.Filename,
					"content_type": f.ContentType,
				}
				if len(f.Base64Data) > 0 {
					m["base64"] = f.Base64Data
				}
				mediaList = append(mediaList, m)
			}
			payload["media"] = mediaList
		}

		// Add Organization Tags
		if toClient != nil {
			for _, num := range toClient.Numbers {
				if strings.Contains(item.To, num.Number) || strings.Contains(num.Number, item.To) {
					if num.Tag != "" {
						payload["tag"] = num.Tag
					}
					if num.Group != "" {
						payload["group"] = num.Group
					}
					break
				}
			}
		}
	}

	// Marshal JSON
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %v", err)
	}

	// Determine timeout - use client setting if set, otherwise global config
	timeoutSecs := router.gateway.Config.WebhookTimeoutSecs
	if toClient != nil && toClient.Settings != nil && toClient.Settings.WebhookTimeoutSecs > 0 {
		timeoutSecs = toClient.Settings.WebhookTimeoutSecs
	}

	// Send HTTP POST
	client := &http.Client{
		Timeout: time.Duration(timeoutSecs) * time.Second,
	}
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Set auth header based on format
	if toClient != nil {
		if apiFormat == "bicom" {
			// Bicom uses Bearer token (base64 of username:password)
			token := base64.StdEncoding.EncodeToString([]byte(toClient.Username + ":" + toClient.Password))
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			// Default to Basic auth
			auth := base64.StdEncoding.EncodeToString([]byte(toClient.Username + ":" + toClient.Password))
			req.Header.Set("Authorization", "Basic "+auth)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute webhook request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook replied with failure status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"regexp"
	"strings"
	"time"
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

	// Lookup clients
	toClient, _ := router.findClientByNumber(m.To)
	fromClient, _ := router.findClientByNumber(m.From)

	if origin == "client" && fromClient == nil {
		lm.SendLog(lm.BuildLog("Router", "Invalid sender number", logrus.ErrorLevel, map[string]interface{}{
			"logID": m.LogID,
			"from":  m.From,
		}))
		return
	}
	// If both client lookups fail, log and return.
	if origin == "carrier" && toClient == nil {
		lm.SendLog(lm.BuildLog("Router", "Invalid destination number", logrus.ErrorLevel, map[string]interface{}{
			"logID": m.LogID,
			"from":  m.From,
		}))
		return
	}

	// Retry on the channel where the message came from
	retryChan := router.ClientMsgChan
	if origin == "carrier" {
		retryChan = router.CarrierMsgChan
	}

	// Process based on message type
	switch m.Type {
	case MsgQueueItemType.SMS:
		if toClient != nil {
			// Try to send via SMPP
			session, err := router.gateway.SMPPServer.findSmppSession(m.To)
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
			// Record the successful send
			internal := (fromClient != nil && toClient != nil)
			if fromClient != nil {
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: *m,
					Carrier:      "from_client",
					ClientID:     fromClient.ID,
					Internal:     internal,
				}
			}
			if toClient != nil {
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: *m,
					Carrier:      "to_client",
					ClientID:     toClient.ID,
					Internal:     internal,
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
						lm.SendLog(lm.BuildLog(
							"ROUTER.SMS",
							"RouterSendCarrier",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": fromClient.Username,
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
					convoID := computeCorrelationKey(m.From, m.To)
					// Update the conversation queue with the expected ack.
					router.gateway.ConvoManager.SetExpectedAck(convoID, ackID, router, 10*time.Second)

					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: *m,
						Carrier:      carrier,
						ClientID:     fromClient.ID,
						Internal:     false,
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
			if fromClient != nil {
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: *m,
					Carrier:      "from_client",
					ClientID:     fromClient.ID,
					Internal:     internal,
				}
			}
			if toClient != nil {
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: *m,
					Carrier:      "to_client",
					ClientID:     toClient.ID,
					Internal:     internal,
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
						lm.SendLog(lm.BuildLog(
							"ROUTER.MMS",
							"RouterSendCarrier",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": fromClient.Username,
								"logID":  m.LogID,
							}, err,
						))

						if m.Retry("failed to send MMS to carrier", retryChan) {
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

						return
					}

					lm.SendLog(lm.BuildLog(
						"Router.SMS",
						"Successfully sent MMS",
						logrus.InfoLevel,
						map[string]interface{}{
							"logID":     m.LogID,
							"carrierID": ackID,
							"from":      m.From,
							"to":        m.To,
						}, nil,
					))

					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: *m,
						Carrier:      carrier,
						ClientID:     fromClient.ID,
						Internal:     false,
					}
					return

				}
			} else {
				lm.SendLog(lm.BuildLog(
					"ROUTER.MMS",
					"RouterFindCarrier",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": fromClient.Username,
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
					"client": fromClient.Username,
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

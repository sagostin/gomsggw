package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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

func (router *Router) ClientMsgConsumer() {
	for {
		client := router.gateway.AMPQClient
		deliveries, err := client.ConsumeMessages("client")
		if err != nil {
			continue
		}

		for delivery := range deliveries {
			var msgQueueItem MsgQueueItem
			err := json.Unmarshal(delivery.Body, &msgQueueItem)
			if err != nil {
				client.logger.Error(err)
				continue
			}
			msgQueueItem.Delivery = &delivery

			//client.logger.Printf("Received message (%v) from %s", delivery.DeliveryTag, delivery.Body)

			router.ClientMsgChan <- msgQueueItem
			// todo only ack if was successful??
		}
	}
}

func (router *Router) CarrierMsgConsumer() {
	for {
		client := router.gateway.AMPQClient
		deliveries, err := client.ConsumeMessages("carrier")
		if err != nil {
			//client.logger.Error(err)
			continue
		}

		for delivery := range deliveries {
			var msgQueueItem MsgQueueItem
			err := json.Unmarshal(delivery.Body, &msgQueueItem)
			if err != nil {
				client.logger.Error(err)
				continue
			}
			msgQueueItem.Delivery = &delivery

			//client.logger.Printf("Received message (%v) from carrier %s", delivery.DeliveryTag, delivery.Body)

			router.CarrierMsgChan <- msgQueueItem
			// todo only ack if was successful??
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

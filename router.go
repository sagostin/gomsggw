package main

import (
	"encoding/json"
	"fmt"
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
			client.logger.Error(err)
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

			/*client.logger.Info("waiting to see if it does anything different?")
			time.Sleep(5 * time.Second)*/
		}
	}
}

func (router *Router) CarrierMsgConsumer() {
	for {
		client := router.gateway.AMPQClient
		deliveries, err := client.ConsumeMessages("carrier")
		if err != nil {
			client.logger.Error(err)
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

			/*client.logger.Info("waiting to see if it does anything different?")
			time.Sleep(5 * time.Second)*/
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

func (router *Router) findClientByNumber(number string) (*Client, error) {
	for _, client := range router.gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return client, nil
			}
		}
	}
	return nil, fmt.Errorf("unable to find client")
}

/*// Build a map of numbers to carriers
func buildNumberToCarrierMap(clients map[string]*Client) map[string]string {
	numberToCarrier := make(map[string]string)
	for _, client := range clients {
		for _, num := range client.Numbers {
			numberToCarrier[num.Number] = num.Carrier
		}
	}
	return numberToCarrier
}

// Then update your findRouteByNumber function
func findRouteByNumberOptimized(sourceNumber string, destinationNumber string, numberToCarrier map[string]string, routes []Route) (*Route, error) {
	destinationCarrier, ok := numberToCarrier[destinationNumber]
	if !ok {
		return nil, fmt.Errorf("carrier not found for destination number: %s", destinationNumber)
	}

	// Now, find the route that matches the carrier
	for _, route := range routes {
		if route.Type == destinationCarrier {
			return &route, nil
		}
	}

	return nil, fmt.Errorf("route not found for carrier: %s", destinationCarrier)
}
*/

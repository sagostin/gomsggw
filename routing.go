package main

import (
	"fmt"
)

// Build a map of numbers to carriers
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

type Route struct {
	Type     string // "carrier" or "smpp"
	Endpoint string
	Handler  CarrierHandler
}

type Routing struct {
	Routes []*Route
}

func (srv *Routing) AddRoute(routeType, endpoint string, handler CarrierHandler) {
	srv.Routes = append(srv.Routes, &Route{Type: routeType, Endpoint: endpoint, Handler: handler})
}

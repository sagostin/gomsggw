package main

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"strings"
	"sync"
)

// Gateway handles SMS processing for different carriers
type Gateway struct {
	Carriers    map[string]CarrierHandler
	MongoClient *mongo.Client
	SMPPServer  *SMPPServer
	Router      *Router
	MM4Server   *MM4Server
	AMPQClient  *AMPQClient
	Clients     map[string]*Client
	mu          sync.RWMutex
}

// NewGateway creates a new Gateway instance
func NewGateway() (*Gateway, error) {
	logf := LoggingFormat{Type: LogType.Startup}
	var gateway = &Gateway{
		Carriers: make(map[string]CarrierHandler),
		Router: &Router{
			Routes:         make([]*Route, 0),
			ClientMsgChan:  make(chan MsgQueueItem),
			CarrierMsgChan: make(chan MsgQueueItem),
		},
		Clients: make(map[string]*Client),
	}

	gateway.Router.gateway = gateway
	/*&Gateway{
		Carriers:    make(map[string]CarrierHandler),
		Router:      &Router{Routes: make([]*Route, 0)},
		MongoClient: mongoClient,
		AMPQClient:  ampqClient,
		Clients: clients,
	}, nil*/

	mongoClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(os.Getenv("MONGODB_URI")))
	if err != nil {
		return nil, err
	}

	gateway.MongoClient = mongoClient

	loadedClients, err := loadClients()
	if err != nil {
		panic(err)
	}

	for _, v := range loadedClients {
		gateway.Clients[v.Username] = &v
	}

	err = loadCarriers(gateway)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "failed to load carriers"
		logf.Print()
		os.Exit(1)
	}

	for _, c := range gateway.Carriers {
		gateway.Router.AddRoute("carrier", c.Name(), c)
	}

	return gateway, nil
}

// getCarrier returns the carrier associated with a phone number.
func (gateway *Gateway) getCarrier(number string) (string, error) {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()
	for _, client := range gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return num.Carrier, nil
			}
		}
	}
	return "", fmt.Errorf("no carrier found for number: %s", number)
}

// getClient returns the client associated with a phone number.
func (gateway *Gateway) getClient(number string) *Client {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()
	for _, client := range gateway.Clients {
		for _, num := range client.Numbers {
			if num.Number == number {
				return client
			}
		}
	}
	return nil
}

func (gateway *Gateway) getClientCarrier(number string) (string, error) {
	for _, client := range gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return num.Carrier, nil
			}
		}
	}

	return "", nil
}

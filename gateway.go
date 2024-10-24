package main

import (
	"context"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Gateway handles SMS processing for different carriers
type Gateway struct {
	Carriers    map[string]CarrierHandler
	MongoClient *mongo.Client
	SMPPServer  *SMPPServer
	Routing     *Routing
	MM4Server   *MM4Server
}

// NewGateway creates a new Gateway instance
func NewGateway(mongoURI string) (*Gateway, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, err
	}

	return &Gateway{
		Carriers:    make(map[string]CarrierHandler),
		Routing:     &Routing{Routes: make([]*Route, 0)},
		MongoClient: client,
	}, nil
}

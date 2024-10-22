package main

import (
	"go.mongodb.org/mongo-driver/mongo"
	"time"
)

// SMS represents a generic SMS message
type SMS struct {
	ID          string            `json:"id" bson:"id"`
	From        string            `json:"from" bson:"from"`
	To          string            `json:"to" bson:"to"`
	Content     string            `json:"content" bson:"content"`
	CarrierData map[string]string `json:"carrier_data" bson:"carrier_data"`
}

// OptOutStatus represents the opt-out status for a sender-receiver pair
type OptOutStatus struct {
	Sender    string    `bson:"sender"`
	Receiver  string    `bson:"receiver"`
	OptedOut  bool      `bson:"opted_out"`
	Timestamp time.Time `bson:"timestamp"`
}

// BaseCarrierHandler provides common functionality for carriers
type BaseCarrierHandler struct {
	name   string
	logger *CustomLogger
}

// Gateway handles SMS processing for different carriers
type Gateway struct {
	Carriers         map[string]CarrierHandler
	MongoClient      *mongo.Client
	OptOutCollection *mongo.Collection
	Logger           *CustomLogger
	SMPPServer       *SMPPServer
	Routing          *Routing
	MM4Server        *MM4Server
}

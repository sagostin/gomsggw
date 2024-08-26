package main

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

/*func (sms *SMS) forwardToJasmin(logger *CustomLogger) error {
	params := url.Values{}
	params.Add("username", os.Getenv("JASMIN_USER")) // Replace with actual username
	params.Add("password", os.Getenv("JASMIN_PASS")) // Replace with actual password
	params.Add("from", sms.From)
	params.Add("to", sms.To)
	params.Add("content", sms.Content)

	// Construct the full URL
	return nil
}*/

// NewSMSGateway creates a new SMSGateway instance
func NewSMSGateway(mongoURI string, logger *CustomLogger) (*SMSGateway, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, err
	}

	database := client.Database("sms_gateway")
	optOutCollection := database.Collection("opt_out_status")

	// Create a compound index on sender and receiver
	_, err = optOutCollection.Indexes().CreateOne(
		context.Background(),
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "sender", Value: 1},
				{Key: "receiver", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
	)
	if err != nil {
		return nil, err
	}

	return &SMSGateway{
		Carriers:         make(map[string]CarrierHandler),
		MongoClient:      client,
		OptOutCollection: optOutCollection,
		Logger:           logger,
	}, nil
}

func (g *SMSGateway) isOptedOut(sender, receiver string) (bool, error) {
	var status OptOutStatus
	err := g.OptOutCollection.FindOne(
		context.Background(),
		bson.M{"sender": sender, "receiver": receiver},
	).Decode(&status)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil // Not in DB, so not opted out
	} else if err != nil {
		return false, err
	}
	return status.OptedOut, nil
}

func (g *SMSGateway) setOptOutStatus(sender, receiver string, optOut bool) error {
	_, err := g.OptOutCollection.UpdateOne(
		context.Background(),
		bson.M{"sender": sender, "receiver": receiver},
		bson.M{"$set": bson.M{"opted_out": optOut, "timestamp": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

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

// NewGateway creates a new Gateway instance
func NewGateway(mongoURI string, logger *CustomLogger) (*Gateway, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, err
	}

	return &Gateway{
		Carriers:    make(map[string]CarrierHandler),
		MongoClient: client,
		Logger:      logger,
	}, nil
}

func (g *Gateway) isOptedOut(sender, receiver string) (bool, error) {
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

func (g *Gateway) setOptOutStatus(sender, receiver string, optOut bool) error {
	_, err := g.OptOutCollection.UpdateOne(
		context.Background(),
		bson.M{"sender": sender, "receiver": receiver},
		bson.M{"$set": bson.M{"opted_out": optOut, "timestamp": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

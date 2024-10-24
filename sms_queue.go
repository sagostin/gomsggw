package main

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"log"
	"os"
	"regexp"
	"time"
)

const (
	SMSQueueDBName         = "gateway_data"
	SMSQueueCollectionName = "sms_queue"
)

// SMSQueueItem represents a queued SMS message.
type SMSQueueItem struct {
	ID          primitive.ObjectID `bson:"_id"`
	From        string             `bson:"from"`
	To          string             `bson:"to"`
	LogID       string             `bson:"logID"`
	Content     string             `bson:"content"`
	Gateway     string             `bson:"gateway"`
	Client      string             `bson:"client"`       // Username or identifier of the client
	Route       string             `bson:"route"`        // Route identifier
	CreatedAt   time.Time          `bson:"created_at"`   // Timestamp when the message was queued
	RetryCount  int                `bson:"retry_count"`  // Number of retry attempts
	LastAttempt time.Time          `bson:"last_attempt"` // Timestamp of the last send attempt
}

// EnqueueSMS stores an SMPPMessage in the MongoDB queue.
func EnqueueSMS(ctx context.Context, collection *mongo.Collection, sms SMPPMessage) error {
	// Validate that logID is present
	if sms.logID == "" {
		return fmt.Errorf("logID is required to enqueue SMS")
	}

	smsItem := SMSQueueItem{
		From:        sms.From,
		To:          sms.To,
		Content:     sms.Content,
		Client:      sms.CarrierData["Client"], // Assuming CarrierData includes "Client"
		Route:       sms.CarrierData["Route"],  // Assuming CarrierData includes "Route"
		LogID:       sms.logID,
		Gateway:     os.Getenv("SERVER_ID"),
		CreatedAt:   time.Now().UTC(),
		RetryCount:  0,
		LastAttempt: time.Time{}, // Zero value
		ID:          primitive.NewObjectID(),
	}

	_, err := collection.InsertOne(ctx, smsItem)
	if err != nil {
		return fmt.Errorf("failed to enqueue SMS: %v", err)
	}

	return nil
}

// DequeueSMS retrieves a batch of SMS messages from the queue for processing.
func DequeueSMS(ctx context.Context, collection *mongo.Collection, batchSize int) ([]SMSQueueItem, error) {
	filter := bson.M{
		"retry_count": bson.M{"$lt": 5}, // Limit retries to 5
	}

	opts := options.Find().
		SetSort(bson.M{"created_at": 1}).
		SetLimit(int64(batchSize))

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue SMS: %v", err)
	}
	defer cursor.Close(ctx)

	var smsItems []SMSQueueItem
	if err = cursor.All(ctx, &smsItems); err != nil {
		return nil, fmt.Errorf("failed to decode dequeued SMS: %v", err)
	}

	return smsItems, nil
}

// IncrementRetryCount increments the retry count and updates the last attempt timestamp.
func IncrementRetryCount(ctx context.Context, collection *mongo.Collection, id primitive.ObjectID) error {
	_, err := collection.UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{
			"$inc": bson.M{"retry_count": 1},
			"$set": bson.M{"last_attempt": time.Now().UTC()},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to increment retry count: %v", err)
	}
	return nil
}

// processReconnectNotifications listens for reconnection notifications and processes queued messages
func (srv *SMPPServer) processReconnectNotifications() {
	for username := range srv.reconnectChannel {
		go func(user string) {
			log.Printf("Processing queued SMS for user: %s", user)
			srv.processQueuedMessagesForUser(user)
		}(username)
	}
}

// processQueuedMessagesForUser fetches and sends queued messages for the given user based on destination numbers
func (srv *SMPPServer) processQueuedMessagesForUser(username string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Retrieve the client's destination numbers
	client, exists := srv.GatewayClients[username]
	if !exists {
		log.Printf("No client found with username: %s", username)
		return
	}

	var destinationNumbers []string
	for _, num := range client.Numbers {
		destinationNumbers = append(destinationNumbers, num.Number)
	}

	// Create regex filters for each destination number
	var regexFilters []bson.M
	for _, number := range destinationNumbers {
		// Example: Match numbers that start with the destination number
		// Adjust the pattern based on your specific matching requirements
		escapedNumber := regexp.QuoteMeta(number)
		pattern := fmt.Sprintf("%s", escapedNumber)
		regexFilters = append(regexFilters, bson.M{
			"to": bson.M{
				"$regex":   pattern,
				"$options": "i", // Case-insensitive; adjust as needed
			},
		})
	}

	// Define the final filter using $or with the regex patterns
	filter := bson.M{
		"$or": regexFilters,
		"retry_count": bson.M{
			"$lt": 5, // Example: max 5 retries
		},
	}

	// Define options: sort by created_at ascending, limit to 100 messages
	opts := options.Find().
		SetSort(bson.M{"created_at": 1}).
		SetLimit(100)

	cursor, err := srv.smsQueueCollection.Find(ctx, filter, opts)
	if err != nil {
		log.Printf("Error fetching queued SMS for user %s: %v", username, err)
		return
	}
	defer cursor.Close(ctx)

	var smsItems []SMSQueueItem
	if err := cursor.All(ctx, &smsItems); err != nil {
		log.Printf("Error decoding queued SMS for user %s: %v", username, err)
		return
	}

	if len(smsItems) == 0 {
		log.Printf("No queued SMS messages for user: %s", username)
		return
	}

	for _, smsItem := range smsItems {
		if smsItem.Gateway != os.Getenv("SERVER_ID") {
			// this we can skip because it was not originally on this
			// this is to cheat using other systems so we can have multiple things
			// share a db because we will load balance with the hook twilio connects from/to

			continue
		}

		// Create SMPPMessage from SMSQueueItem
		sms := SMPPMessage{
			From:    smsItem.From,
			To:      smsItem.To,
			Content: smsItem.Content,
			CarrierData: map[string]string{
				"Client": smsItem.Client,
				"Route":  smsItem.Route,
			},
			logID: smsItem.LogID,
		}

		// Attempt to send the SMS via SMPP
		srv.sendSMPP(sms)
		// On successful send, remove the message from the queue
		_, err := srv.smsQueueCollection.DeleteOne(ctx, bson.M{"_id": smsItem.ID})
		if err != nil {
			log.Printf("Failed to remove SMS (LogID: %s) from queue after sending: %v", smsItem.LogID, err)
			continue
		}

		log.Printf("Successfully sent and removed queued SMS (LogID: %s) for user %s", smsItem.LogID, username)
	}
}

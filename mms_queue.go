// mm4_queue.go
package main

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"net/textproto"
	"time"
)

const (
	MM4QueueDBName         = "gateway_data"
	MM4QueueCollectionName = "mm4_queue"
)

// MM4QueueItem represents a queued MM4 message.
type MM4QueueItem struct {
	ID          primitive.ObjectID  `bson:"_id"`
	From        string              `bson:"from"`
	To          string              `bson:"to"`
	LogID       string              `bson:"logID"`
	Content     []byte              `bson:"content"`
	Headers     map[string][]string `bson:"headers"`
	Client      string              `bson:"client"`       // Username or identifier of the client
	Route       string              `bson:"route"`        // Route identifier
	CreatedAt   time.Time           `bson:"created_at"`   // Timestamp when the message was queued
	RetryCount  int                 `bson:"retry_count"`  // Number of retry attempts
	LastAttempt time.Time           `bson:"last_attempt"` // Timestamp of the last send attempt
}

// EnqueueMM4Message stores an MM4Message in the MongoDB queue.
func EnqueueMM4Message(ctx context.Context, collection *mongo.Collection, mm4 MM4Message) error {
	// Validate that LogID is present
	if mm4.logID == "" {
		return fmt.Errorf("logID is required to enqueue MM4 message")
	}

	mm4Item := MM4QueueItem{
		From:        mm4.From,
		To:          mm4.To,
		Content:     mm4.Content,
		Headers:     convertHeaders(mm4.Headers),
		Client:      mm4.Client.Username, // Assuming Client has Username
		Route:       "",                  // You can set this based on your routing logic
		LogID:       mm4.logID,
		CreatedAt:   time.Now().UTC(),
		RetryCount:  0,
		LastAttempt: time.Time{}, // Zero value
		ID:          primitive.NewObjectID(),
	}

	_, err := collection.InsertOne(ctx, mm4Item)
	if err != nil {
		return fmt.Errorf("failed to enqueue MM4 message: %v", err)
	}

	return nil
}

// DequeueMM4Messages retrieves a batch of MM4 messages from the queue for processing.
func DequeueMM4Messages(ctx context.Context, collection *mongo.Collection, batchSize int) ([]MM4QueueItem, error) {
	filter := bson.M{
		"retry_count": bson.M{"$lt": 5}, // Limit retries to 5
	}

	opts := options.Find().
		SetSort(bson.M{"created_at": 1}).
		SetLimit(int64(batchSize))

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue MM4 messages: %v", err)
	}
	defer cursor.Close(ctx)

	var mm4Items []MM4QueueItem
	if err = cursor.All(ctx, &mm4Items); err != nil {
		return nil, fmt.Errorf("failed to decode dequeued MM4 messages: %v", err)
	}

	return mm4Items, nil
}

// IncrementMM4RetryCount increments the retry count and updates the last attempt timestamp.
func IncrementMM4RetryCount(ctx context.Context, collection *mongo.Collection, id primitive.ObjectID) error {
	_, err := collection.UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{
			"$inc": bson.M{"retry_count": 1},
			"$set": bson.M{"last_attempt": time.Now().UTC()},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to increment MM4 retry count: %v", err)
	}
	return nil
}

// RemoveMM4Message removes a successfully processed MM4 message from the queue.
func RemoveMM4Message(ctx context.Context, collection *mongo.Collection, id primitive.ObjectID) error {
	_, err := collection.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return fmt.Errorf("failed to remove MM4 message from queue: %v", err)
	}
	return nil
}

// Helper function to convert textproto.MIMEHeader to map[string][]string
func convertHeaders(headers textproto.MIMEHeader) map[string][]string {
	converted := make(map[string][]string)
	for key, values := range headers {
		converted[key] = values
	}
	return converted
}

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"time"
)

const (
	DBName         = "media_storage"
	CollectionName = "media_files"
	TTLDuration    = 7 * 24 * time.Hour // 7-day expiration
)

type MediaFile struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	FileName    string    `json:"file_name" bson:"file_name"`
	ContentType string    `json:"content_type" bson:"content_type"`
	Base64Data  string    `json:"base64_data" bson:"base64_data"`
	UploadAt    time.Time `json:"upload_at" bson:"upload_at"`
	ExpiresAt   time.Time `json:"expires_at" bson:"expires_at"` // TTL expiration field
}

// SaveBase64ToMongoDB stores base64-encoded data as a document with a 7-day expiration
func saveBase64ToMongoDB(client *mongo.Client, fileName string, base64Data string, contentType string) (string, error) {
	collection := client.Database(DBName).Collection(CollectionName)

	// Create the document with base64 data and expiration date
	mediaFile := MediaFile{
		FileName:    fileName,
		ContentType: contentType,
		Base64Data:  base64Data,
		UploadAt:    time.Now(),
		ExpiresAt:   time.Now().Add(TTLDuration), // Set expiration 7 days from now
	}

	// Insert the document into the collection
	result, err := collection.InsertOne(context.Background(), mediaFile)
	if err != nil {
		return "", fmt.Errorf("failed to insert media file to db: %s", fileName)
	}

	// Type assert the InsertedID to primitive.TransactionID
	insertedID, ok := result.InsertedID.(primitive.ObjectID)
	if !ok {
		return "", fmt.Errorf("failed to convert inserted id: %s", insertedID)
	}

	// Get the hexadecimal string representation of the TransactionID
	return insertedID.Hex(), nil
}

// GetMediaFromMongoDB retrieves a media file document from MongoDB
func getMediaFromMongoDB(client *mongo.Client, fileID string) (*MediaFile, error) {
	collection := client.Database(DBName).Collection(CollectionName)

	hex, err := primitive.ObjectIDFromHex(fileID)
	if err != nil {
		return nil, err
	}

	var mediaFile MediaFile
	err = collection.FindOne(context.Background(), bson.M{"_id": hex}).Decode(&mediaFile)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve media file: %s", fileID)
	}

	return &mediaFile, nil
}

// DecodeBase64Media decodes the base64 media content into raw binary data
func decodeBase64Media(base64Data string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 data: %v", err)
	}
	return data, nil
}

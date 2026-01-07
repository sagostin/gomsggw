package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	TTLDuration = 7 * 24 * time.Hour // 7-day expiration
)

// MediaFile stores uploaded media content with UUID-based access tokens
type MediaFile struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccessToken string    `gorm:"uniqueIndex;size:36" json:"access_token"` // UUID for secure access
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	Base64Data  string    `json:"base64_data"`
	UploadAt    time.Time `json:"upload_at"`
	ExpiresAt   time.Time `gorm:"index" json:"expires_at"`
}

func (gateway *Gateway) uploadMediaGetUrls(mms *MsgQueueItem) ([]string, error) {
	var mediaUrls []string

	if len(mms.files) > 0 {
		for _, i := range mms.files {
			if strings.Contains(i.ContentType, "application/smil") {
				continue
			}

			accessToken, err := gateway.saveMsgFileMedia(i)
			if err != nil {
				return mediaUrls, err
			}

			mediaUrls = append(mediaUrls, os.Getenv("SERVER_ADDRESS")+"/media/"+accessToken)
		}
		return mediaUrls, nil
	}
	return mediaUrls, nil
}

func (gateway *Gateway) cleanUpExpiredMediaFiles(interval time.Duration) {
	// Run cleanup immediately
	err := gateway.DB.Where("expires_at < ?", time.Now()).Delete(&MediaFile{}).Error
	if err != nil {
		fmt.Printf("Failed to clean up expired media files: %v\n", err)
	}

	// Create a ticker that fires at the specified interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		err := gateway.DB.Where("expires_at < ?", time.Now()).Delete(&MediaFile{}).Error
		if err != nil {
			fmt.Printf("Failed to clean up expired media files: %v\n", err)
		}
	}
}

// saveMsgFileMedia saves a media file and returns its UUID access token
func (gateway *Gateway) saveMsgFileMedia(file MsgFile) (string, error) {
	accessToken := uuid.New().String()

	mediaFile := MediaFile{
		AccessToken: accessToken,
		FileName:    file.Filename,
		ContentType: file.ContentType,
		Base64Data:  file.Base64Data,
		UploadAt:    time.Now(),
		ExpiresAt:   time.Now().Add(TTLDuration),
	}

	if err := gateway.DB.Create(&mediaFile).Error; err != nil {
		return "", fmt.Errorf("failed to insert media file to db: %v", err)
	}

	return accessToken, nil
}

// getMediaFileByToken retrieves a media file by its UUID access token
func (gateway *Gateway) getMediaFileByToken(accessToken string) (*MediaFile, error) {
	var mediaFile MediaFile
	if err := gateway.DB.Where("access_token = ?", accessToken).First(&mediaFile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("media file not found: %s", accessToken)
		}
		return nil, fmt.Errorf("failed to retrieve media file: %v", err)
	}

	// Check if the media file has expired
	if time.Now().After(mediaFile.ExpiresAt) {
		// Delete the expired media file
		gateway.DB.Delete(&mediaFile)
		return nil, fmt.Errorf("media file has expired: %s", accessToken)
	}

	return &mediaFile, nil
}

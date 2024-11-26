package main

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	TTLDuration = 7 * 24 * time.Hour // 7-day expiration
)

func (gateway *Gateway) uploadMediaGetUrls(mms *MsgQueueItem) ([]string, error) {
	var mediaUrls []string

	if len(mms.Files) > 0 {
		for _, i := range mms.Files {
			if strings.Contains(i.ContentType, "application/smil") {
				continue
			}

			id, err := gateway.saveMsgFileMedia(i)
			if err != nil {
				return mediaUrls, err
			}

			mediaUrls = append(mediaUrls, os.Getenv("SERVER_ADDRESS")+"/media/"+strconv.Itoa(int(id)))
		}
		return mediaUrls, nil
	}
	return mediaUrls, nil
}

type MediaFile struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	Base64Data  string    `json:"base64_data"`
	UploadAt    time.Time `json:"upload_at"`
	ExpiresAt   time.Time `gorm:"index" json:"expires_at"`
}

func (gateway *Gateway) cleanUpExpiredMediaFiles() {
	ticker := time.NewTicker(24 * time.Hour) // Adjust the interval as needed
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := gateway.DB.Where("expires_at < ?", time.Now()).Delete(&MediaFile{}).Error
			if err != nil {
				// Log the error
				fmt.Printf("Failed to clean up expired media files: %v\n", err)
			}
		}
	}
}

func (gateway *Gateway) saveMsgFileMedia(file MsgFile) (uint, error) {
	mediaFile := MediaFile{
		FileName:    file.Filename,
		ContentType: file.ContentType,
		Base64Data:  string(file.Content),
		UploadAt:    time.Now(),
		ExpiresAt:   time.Now().Add(TTLDuration),
	}

	if err := gateway.DB.Create(&mediaFile).Error; err != nil {
		return 0, fmt.Errorf("failed to insert media file to db: %v", err)
	}

	return mediaFile.ID, nil
}

func (gateway *Gateway) getMediaFile(fileID uint) (*MediaFile, error) {
	var mediaFile MediaFile
	if err := gateway.DB.First(&mediaFile, fileID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("media file not found: %d", fileID)
		}
		return nil, fmt.Errorf("failed to retrieve media file: %v", err)
	}

	// Check if the media file has expired
	if time.Now().After(mediaFile.ExpiresAt) {
		// Optionally delete the expired media file
		gateway.DB.Delete(&mediaFile)
		return nil, fmt.Errorf("media file has expired: %d", fileID)
	}

	return &mediaFile, nil
}

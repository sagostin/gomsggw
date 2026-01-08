package main

import (
	"time"

	"github.com/sirupsen/logrus"
)

func (gateway *Gateway) processMsgRecords() {
	for {
		msg := <-gateway.MsgRecordChan
		if msg.ClientID == 0 {
			continue
		}
		err := gateway.InsertMsgRecord(msg)
		if err != nil {
			gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
				"MsgRecords",
				"InsertError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID":    msg.MsgQueueItem.LogID,
					"clientID": msg.ClientID,
				}, err,
			))
			continue
		}

		gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
			"MsgRecords",
			"InsertSuccess",
			logrus.DebugLevel,
			map[string]interface{}{
				"logID":    msg.MsgQueueItem.LogID,
				"clientID": msg.ClientID,
			},
		))
	}
}

// MsgRecordDBItem represents the structure for storing messages in the database.
type MsgRecordDBItem struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	ClientID          uint      `gorm:"index;not null" json:"client_id"`
	To                string    `json:"to_number"`
	From              string    `json:"from_number"`
	ReceivedTimestamp time.Time `json:"received_timestamp"`
	Type              string    `json:"type"`              // "mms" or "sms"
	Carrier           string    `json:"carrier,omitempty"` // Carrier name (optional)
	Internal          bool      `json:"internal"`          // Whether the message is internal (client to client)
	LogID             string    `json:"log_id"`
	ServerID          string    `json:"server_id"`

	// Enhanced tracking fields
	Direction      string `json:"direction"`           // "inbound" or "outbound" relative to gateway
	FromClientType string `json:"from_client_type"`    // "legacy", "web", or "carrier"
	ToClientType   string `json:"to_client_type"`      // "legacy", "web", or "carrier"
	DeliveryMethod string `json:"delivery_method"`     // "smpp", "mm4", "webhook", "carrier_api"
	SourceIP       string `json:"source_ip,omitempty"` // Originating IP address (for web/API messages)

	// SMS specific
	Encoding            string `json:"encoding,omitempty"`              // "gsm7", "ucs2", "ascii"
	TotalSegments       int    `json:"total_segments"`                  // Total segments in this message (all share same LogID)
	OriginalBytesLength int    `json:"original_bytes_length,omitempty"` // Original SMS byte length

	// MMS specific
	OriginalSizeBytes    int  `json:"original_size_bytes,omitempty"`   // Total media size before transcoding
	TranscodedSizeBytes  int  `json:"transcoded_size_bytes,omitempty"` // Total media size after transcoding
	MediaCount           int  `json:"media_count,omitempty"`           // Number of media attachments
	TranscodingPerformed bool `json:"transcoding_performed,omitempty"` // Whether transcoding was applied
}

// PartiallyRedactMessage redacts part of the message for privacy.
func PartiallyRedactMessage(message string) string {
	if len(message) <= 10 {
		return "**********" // Fully redacted for short messages.
	}
	return message[:5] + "*****" // Partially redact, keep first 5 characters.
}

// InsertMsgQueueItem inserts a MsgQueueItem into the database with enhanced tracking.
func (gateway *Gateway) InsertMsgRecord(record MsgRecord) error {
	item := record.MsgQueueItem

	// Calculate media sizes if MMS
	var transcodedSize, mediaCount int
	if item.Type == MsgQueueItemType.MMS && len(item.files) > 0 {
		mediaCount = len(item.files)
		for _, f := range item.files {
			if len(f.Content) > 0 {
				transcodedSize += len(f.Content)
			}
			if len(f.Base64Data) > 0 {
				// Base64 decodes to ~75% of encoded size
				transcodedSize += len(f.Base64Data) * 3 / 4
			}
		}
	}

	dbItem := &MsgRecordDBItem{
		To:                item.To,
		From:              item.From,
		ClientID:          record.ClientID,
		ReceivedTimestamp: item.ReceivedTimestamp,
		Type:              string(item.Type),
		Carrier:           record.Carrier,
		Internal:          record.Internal,
		LogID:             item.LogID,
		ServerID:          gateway.ServerID,

		// Enhanced tracking
		Direction:            record.Direction,
		FromClientType:       record.FromClientType,
		ToClientType:         record.ToClientType,
		DeliveryMethod:       record.DeliveryMethod,
		SourceIP:             record.SourceIP,
		Encoding:             record.Encoding,
		TotalSegments:        record.TotalSegments,
		OriginalBytesLength:  record.OriginalBytesLength,
		OriginalSizeBytes:    record.OriginalSizeBytes,
		TranscodedSizeBytes:  transcodedSize,
		MediaCount:           mediaCount,
		TranscodingPerformed: record.TranscodingPerformed,
	}

	if err := gateway.DB.Create(dbItem).Error; err != nil {
		return err
	}

	return nil
}

// InsertMsgQueueItem inserts a MsgQueueItem into the database (legacy compatibility).
func (gateway *Gateway) InsertMsgQueueItem(item MsgQueueItem, clientID uint, carrier string, internal bool) error {
	return gateway.InsertMsgRecord(MsgRecord{
		MsgQueueItem: item,
		ClientID:     clientID,
		Carrier:      carrier,
		Internal:     internal,
	})
}

func swapToAndFrom(item MsgQueueItem) MsgQueueItem {
	newMsg := item
	newMsg.From = item.To
	newMsg.To = item.From
	return newMsg
}

// GetUsageCount retrieves the number of messages sent by a client or from a specific number within a time period.
// If number is empty, it returns the total count for the client.
func (gateway *Gateway) GetUsageCount(clientID uint, number string, since time.Time) (int64, error) {
	var count int64
	query := gateway.DB.Model(&MsgRecordDBItem{}).
		Where("client_id = ? AND received_timestamp >= ?", clientID, since)

	// If scanning for a specific number, filter by 'From' address
	if number != "" {
		// Note: The number stored in DB might have formatting, so we might need fuzzy match or exact depending on router logic.
		// For now, assuming exact match on normalized number.
		query = query.Where("`from` = ?", number)
	}

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// GetUsageCountByType retrieves usage filtered by message type (sms/mms).
func (gateway *Gateway) GetUsageCountByType(clientID uint, number string, msgType string, since time.Time) (int64, error) {
	var count int64
	query := gateway.DB.Model(&MsgRecordDBItem{}).
		Where("client_id = ? AND received_timestamp >= ? AND type = ?", clientID, since, msgType)

	if number != "" {
		query = query.Where("`from` = ?", number)
	}

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// GetUsageCountWithDirection retrieves usage filtered by message type and direction.
// direction can be "outbound", "inbound", or "" for both
func (gateway *Gateway) GetUsageCountWithDirection(clientID uint, number string, msgType string, direction string, since time.Time) (int64, error) {
	var count int64
	query := gateway.DB.Model(&MsgRecordDBItem{}).
		Where("client_id = ? AND received_timestamp >= ? AND type = ?", clientID, since, msgType)

	if number != "" {
		if direction == "outbound" {
			query = query.Where("`from` = ?", number)
		} else if direction == "inbound" {
			query = query.Where("`to` = ?", number)
		} else {
			// Both: either from or to matches
			query = query.Where("(`from` = ? OR `to` = ?)", number, number)
		}
	}

	if direction != "" && direction != "both" {
		query = query.Where("direction = ?", direction)
	}

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

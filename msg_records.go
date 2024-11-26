package main

import (
	"time"
)

func (gateway *Gateway) processMsgRecords() {
	for {
		msg := <-gateway.MsgRecordChan
		if msg.ClientID == 0 {
			continue
		}
		err := gateway.InsertMsgQueueItem(msg.MsgQueueItem, msg.ClientID, msg.Carrier, msg.Internal)
		if err != nil {
			// todo logging
			continue
		}
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
	RedactedMessage   string    `json:"redacted_message"`  // Partially redacted message
	Carrier           string    `json:"carrier,omitempty"` // Carrier name (optional)
	Internal          bool      `json:"internal"`          // Whether the message is internal
	LogID             string    `json:"log_id"`
	ServerID          string
}

// PartiallyRedactMessage redacts part of the message for privacy.
func PartiallyRedactMessage(message string) string {
	if len(message) <= 10 {
		return "**********" // Fully redacted for short messages.
	}
	return message[:5] + "*****" // Partially redact, keep first 5 characters.
}

// InsertMsgQueueItem inserts a MsgQueueItem into the database.
func (gateway *Gateway) InsertMsgQueueItem(item MsgQueueItem, clientID uint, carrier string, internal bool) error {
	// Convert MsgQueueItem to MsgRecordDBItem with redacted message.
	dbItem := &MsgRecordDBItem{
		To:                item.To,
		From:              item.From,
		ClientID:          clientID,
		ReceivedTimestamp: item.ReceivedTimestamp,
		//QueuedTimestamp:   item.QueuedTimestamp,
		Type:            string(item.Type),
		RedactedMessage: PartiallyRedactMessage(item.Message),
		Carrier:         carrier,
		Internal:        internal,
		LogID:           item.LogID,
		ServerID:        gateway.ServerID,
	}

	if err := gateway.DB.Create(dbItem).Error; err != nil {
		return err
	}

	// Insert into the database using InsertStruct.
	return nil
}

func swapToAndFrom(item MsgQueueItem) MsgQueueItem {
	newMsg := item
	newMsg.From = item.To
	newMsg.To = item.From
	return newMsg
}

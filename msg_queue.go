package main

import "time"

type MsgQueueType string

var MsgQueueItemType = struct {
	SMS MsgQueueType
	MMS MsgQueueType
}{
	SMS: "sms",
	MMS: "mms",
}

type MsgQueueItem struct {
	To                string       `json:"to_number"`
	From              string       `json:"from_number"`
	ReceivedTimestamp time.Time    `json:"received_timestamp"`
	QueuedTimestamp   time.Time    `json:"queued_timestamp"`
	Type              MsgQueueType `json:"type"` // mms or sms
	files             []MsgFile
	message           string
	SkipNumberCheck   bool
	LogID             string `json:"log_id"`
	SourceCarrier     string // Carrier name for inbound messages from carrier (e.g., "telnyx")
	SourceIP          string // Originating IP address for web/API messages
	OriginalSizeBytes int    // Original media size before transcoding (MMS only)
	//Delivery          *amqp.Delivery
	Delivery *MsgQueueDelivery
}

type MsgQueueDelivery struct {
	Error      string
	RetryTime  time.Time
	RetryCount int
}

// Retry returns true if discarded
func (msg *MsgQueueItem) Retry(err string, queue chan MsgQueueItem) bool {
	// todo check if the retry count is already set, same with the time, etc.
	if msg.Delivery == nil {
		msg.Delivery = &MsgQueueDelivery{
			Error:      "",
			RetryTime:  time.Now(),
			RetryCount: 0,
		}
	}

	if msg.Delivery.RetryCount == 666 {
		// black hole failure retries
		return false
	}

	if msg.Delivery.RetryCount >= 3 {
		// discard if already tried 3 times

		// this will return true on discard, but we want to send the copy of the message pointer to a "failure"
		// channel so that we can reverse the to/from and send an error to the client that sent it if the carrier fails

		return true
	}

	msg.Delivery.RetryCount++

	if err != "" {
		msg.Delivery.Error = err
	}
	// sleep for retry
	time.Sleep(10 * time.Second)
	queue <- *msg
	return false
}

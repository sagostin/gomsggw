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
	//Delivery          *amqp.Delivery
	Delivery *MsgQueueDelivery
}

type MsgQueueDelivery struct {
	Error      string
	RetryTime  time.Time
	RetryCount int
}

func (msg *MsgQueueItem) Retry(err string, queue chan MsgQueueItem) {
	// todo check if the retry count is already set, same with the time, etc.
	if msg.Delivery == nil {
		msg.Delivery = &MsgQueueDelivery{
			Error:      "",
			RetryTime:  time.Now(),
			RetryCount: 0,
		}
	}

	if msg.Delivery.RetryCount >= 3 {
		// discard if already tried 3 times
		return
	}

	msg.Delivery.RetryCount++

	if err != "" {
		msg.Delivery.Error = err
	}
	// sleep for retry
	time.Sleep(10 * time.Second)
	queue <- *msg
	return
}

package main

import (
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ConvoQueue holds all messages for a given conversation and tracks the active one.
type ConvoQueue struct {
	queue         []MsgQueueItem
	inFlight      bool
	expectedAckID string
	ackTimer      *time.Timer // new field for the ack timeout timer
	mu            sync.Mutex
}

// ConvoManager maps conversation IDs (hashes) to their ConvoQueue and maintains a global ack mapping.
type ConvoManager struct {
	queues map[string]*ConvoQueue
	// ackMap maps carrier ackID -> conversation ID.
	ackMap map[string]string
	mu     sync.Mutex
}

func NewConvoManager() *ConvoManager {
	return &ConvoManager{
		queues: make(map[string]*ConvoQueue),
		ackMap: make(map[string]string),
	}
}

// AddMessage appends a message to the conversation's queue.
// If no message is active, it immediately releases the first message
// by sending it to ClientMsgChan.
func (cm *ConvoManager) AddMessage(convoID string, msg MsgQueueItem, router *Router) {
	lm := router.gateway.LogManager

	cm.mu.Lock()
	cq, exists := cm.queues[convoID]
	if !exists {
		cq = &ConvoQueue{
			queue: make([]MsgQueueItem, 0),
		}
		cm.queues[convoID] = cq
	}
	cm.mu.Unlock()

	cq.mu.Lock()
	cq.queue = append(cq.queue, msg)

	// Debug: Log message addition to convo queue
	queueLen := len(cq.queue)
	wasInFlight := cq.inFlight

	if !cq.inFlight {
		cq.inFlight = true
		nextMsg := cq.queue[0]
		cq.queue = cq.queue[1:]

		// Debug: Log that we're dispatching immediately
		lm.SendLog(lm.BuildLog(
			"ConvoManager.DEBUG",
			"MessageDispatchedImmediately",
			logrus.DebugLevel,
			map[string]interface{}{
				"logID":        msg.LogID,
				"convoID":      convoID,
				"from":         msg.From,
				"to":           msg.To,
				"queueLen":     queueLen,
				"wasInFlight":  wasInFlight,
				"dispatchedTo": "ClientMsgChan",
			},
		))

		// Send the message into the normal routing path.
		router.ClientMsgChan <- nextMsg
	} else {
		// Debug: Log that message was queued
		lm.SendLog(lm.BuildLog(
			"ConvoManager.DEBUG",
			"MessageQueued",
			logrus.DebugLevel,
			map[string]interface{}{
				"logID":       msg.LogID,
				"convoID":     convoID,
				"from":        msg.From,
				"to":          msg.To,
				"queueLen":    queueLen,
				"wasInFlight": wasInFlight,
			},
		))
	}
	cq.mu.Unlock()
}

// SetExpectedAck stores the expected ack for the active message in a conversation,
// records the mapping ackID -> convoID, creates a timer, and starts waiting for it.
func (cm *ConvoManager) SetExpectedAck(convoID, ackID string, router *Router, timeout time.Duration) {
	// Record the mapping of ackID to convoID.
	cm.mu.Lock()
	cm.ackMap[ackID] = convoID
	cm.mu.Unlock()

	// Retrieve the conversation queue.
	cm.mu.Lock()
	cq, exists := cm.queues[convoID]
	cm.mu.Unlock()
	if !exists {
		return
	}

	// Lock the conversation queue to update expectedAckID and its timer.
	cq.mu.Lock()
	cq.expectedAckID = ackID
	// If an old timer exists, stop it.
	if cq.ackTimer != nil {
		cq.ackTimer.Stop()
	}
	// Create a new timer.
	cq.ackTimer = time.NewTimer(timeout)
	cq.mu.Unlock()

	// Start a goroutine that waits on the timer.
	go func() {
		<-cq.ackTimer.C
		router.gateway.LogManager.SendLog(
			router.gateway.LogManager.BuildLog("ConvoManager", "Ack timeout", logrus.WarnLevel,
				map[string]interface{}{"convo": convoID, "expectedAck": ackID}))
		// Process timeout as if the ack was received.
		cm.HandleAck(convoID, ackID, router)
	}()
}

// HandleAck is called when an ack is to be processed for a conversation.
func (cm *ConvoManager) HandleAck(convoID, receivedAck string, router *Router) {
	// Retrieve the conversation queue.
	cm.mu.Lock()
	cq, exists := cm.queues[convoID]
	cm.mu.Unlock()
	if !exists {
		return
	}

	cq.mu.Lock()
	// Stop and clear the timer if it's still running.
	if cq.ackTimer != nil {
		cq.ackTimer.Stop()
		cq.ackTimer = nil
	}
	// Check if the ack matches the expected one.
	if cq.inFlight && cq.expectedAckID == receivedAck {
		// Clear current in-flight state.
		cq.inFlight = false
		cq.expectedAckID = ""
		// If there are queued messages, release the next one.
		if len(cq.queue) > 0 {
			nextMsg := cq.queue[0]
			cq.queue = cq.queue[1:]
			cq.inFlight = true
			router.ClientMsgChan <- nextMsg
		}
	}
	cq.mu.Unlock()
}

// HandleCarrierAck looks up the conversation using the ackID from the carrier,
// then calls HandleAck for that conversation.
func (cm *ConvoManager) HandleCarrierAck(ackID string, router *Router) {
	cm.mu.Lock()
	convoID, exists := cm.ackMap[ackID]
	if exists {
		delete(cm.ackMap, ackID)
	}
	cm.mu.Unlock()
	if !exists {
		router.gateway.LogManager.SendLog(
			router.gateway.LogManager.BuildLog("ConvoManager", "Received unknown ack", logrus.WarnLevel,
				map[string]interface{}{"ackID": ackID}))
		return
	}
	cm.HandleAck(convoID, ackID, router)
}

// computeCorrelationKey creates a conversation ID (hash) from from/to.
// In production, consider using SHA-256. Here we use a simple lower-case concatenation.
func computeCorrelationKey(from, to string) string {
	return strings.ToLower(from) + "_" + strings.ToLower(to)
}

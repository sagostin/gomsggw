package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRouter constructs a minimal Router+Gateway pair suitable for
// exercising ConvoManager without standing up a real PostgreSQL connection.
// ClientMsgChan is buffered so AddMessage can push without blocking.
func newTestRouter(bufferSize int) (*Router, *Gateway) {
	gw := &Gateway{
		LogManager: NewLogManager(NewLokiClient("", "", ""), false),
	}
	r := &Router{
		gateway:        gw,
		ClientMsgChan:  make(chan MsgQueueItem, bufferSize),
		CarrierMsgChan: make(chan MsgQueueItem, bufferSize),
	}
	gw.Router = r
	return r, gw
}

func TestComputeCorrelationKey(t *testing.T) {
	// Lower-cased concatenation with underscore separator
	assert.Equal(t, "+15551234567_+15557654321",
		computeCorrelationKey("+15551234567", "+15557654321"))
	assert.Equal(t, "a_b", computeCorrelationKey("A", "B"))
}

func TestConvoManager_FirstMessageGoesImmediately(t *testing.T) {
	r, _ := newTestRouter(1)
	cm := NewConvoManager()

	msg := MsgQueueItem{LogID: "m1", message: "hello"}
	cm.AddMessage("c1", msg, r)

	select {
	case got := <-r.ClientMsgChan:
		assert.Equal(t, "m1", got.LogID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected immediate dispatch on empty convo")
	}
}

func TestConvoManager_QueuesWhenInFlight(t *testing.T) {
	r, _ := newTestRouter(4)
	cm := NewConvoManager()

	// First message goes straight to ClientMsgChan and the convo is now in-flight.
	cm.AddMessage("c1", MsgQueueItem{LogID: "m1", message: "first"}, r)
	<-r.ClientMsgChan

	// Second message while the first is still in flight must be queued, not dispatched.
	cm.AddMessage("c1", MsgQueueItem{LogID: "m2", message: "second"}, r)
	cm.AddMessage("c1", MsgQueueItem{LogID: "m3", message: "third"}, r)

	select {
	case got := <-r.ClientMsgChan:
		t.Fatalf("expected nothing to be dispatched while in-flight, got %s", got.LogID)
	case <-time.After(50 * time.Millisecond):
		// Good — channel is empty.
	}

	// HandleAck only releases the next message if the ack matches the
	// expected one. We must set it first.
	cm.SetExpectedAck("c1", "ack1", r, 5*time.Second)
	cm.HandleAck("c1", "ack1", r)

	select {
	case got := <-r.ClientMsgChan:
		assert.Equal(t, "m2", got.LogID, "first queued message should dispatch after ack")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected m2 to dispatch after ack")
	}

	cm.SetExpectedAck("c1", "ack2", r, 5*time.Second)
	cm.HandleAck("c1", "ack2", r)
	select {
	case got := <-r.ClientMsgChan:
		assert.Equal(t, "m3", got.LogID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected m3 to dispatch after second ack")
	}

	// No more queued messages — nothing to dispatch.
	cm.HandleAck("c1", "ack3", r)
	select {
	case got := <-r.ClientMsgChan:
		t.Fatalf("expected nothing more to dispatch, got %s", got.LogID)
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestConvoManager_HandleFailureReleasesNext(t *testing.T) {
	r, _ := newTestRouter(4)
	cm := NewConvoManager()

	cm.AddMessage("c1", MsgQueueItem{LogID: "m1", message: "first"}, r)
	<-r.ClientMsgChan

	cm.AddMessage("c1", MsgQueueItem{LogID: "m2", message: "second"}, r)

	// Failure (no ack) should still release the next queued message.
	cm.HandleFailure("c1", r)

	select {
	case got := <-r.ClientMsgChan:
		assert.Equal(t, "m2", got.LogID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected HandleFailure to release the next message")
	}
}

func TestConvoManager_HandleFailureIsNoOpWhenIdle(t *testing.T) {
	r, _ := newTestRouter(1)
	cm := NewConvoManager()

	// No messages added → no panic, no dispatch.
	cm.HandleFailure("does-not-exist", r)

	select {
	case got := <-r.ClientMsgChan:
		t.Fatalf("expected no dispatch, got %s", got.LogID)
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestConvoManager_HandleAck_Mismatch(t *testing.T) {
	r, _ := newTestRouter(2)
	cm := NewConvoManager()

	cm.AddMessage("c1", MsgQueueItem{LogID: "m1", message: "first"}, r)
	<-r.ClientMsgChan
	cm.AddMessage("c1", MsgQueueItem{LogID: "m2", message: "second"}, r)

	// Mismatched ack ID must NOT release the queued message.
	cm.HandleAck("c1", "wrong-ack", r)

	select {
	case got := <-r.ClientMsgChan:
		t.Fatalf("mismatched ack should not release queued message, got %s", got.LogID)
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestConvoManager_CarrierAckLookup(t *testing.T) {
	r, _ := newTestRouter(4)
	cm := NewConvoManager()

	cm.AddMessage("c1", MsgQueueItem{LogID: "m1", message: "first"}, r)
	<-r.ClientMsgChan
	cm.AddMessage("c1", MsgQueueItem{LogID: "m2", message: "second"}, r)
	cm.SetExpectedAck("c1", "carrier-ack-xyz", r, 5*time.Second)

	// Simulate the carrier webhook arriving with the ack id.
	cm.HandleCarrierAck("carrier-ack-xyz", r)

	select {
	case got := <-r.ClientMsgChan:
		assert.Equal(t, "m2", got.LogID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("HandleCarrierAck should have released m2")
	}
}

func TestConvoManager_UnknownCarrierAckIsIgnored(t *testing.T) {
	r, _ := newTestRouter(1)
	cm := NewConvoManager()

	// No prior SetExpectedAck → unknown ack should not panic and not dispatch.
	cm.HandleCarrierAck("never-registered", r)

	select {
	case got := <-r.ClientMsgChan:
		t.Fatalf("expected nothing, got %s", got.LogID)
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestConvoManager_ConversationsAreIsolated(t *testing.T) {
	r, _ := newTestRouter(8)
	cm := NewConvoManager()

	// Two independent conversations, each with one immediate + one queued message.
	cm.AddMessage("c1", MsgQueueItem{LogID: "c1m1"}, r)
	cm.AddMessage("c2", MsgQueueItem{LogID: "c2m1"}, r)
	<-r.ClientMsgChan
	<-r.ClientMsgChan

	cm.AddMessage("c1", MsgQueueItem{LogID: "c1m2"}, r)
	cm.AddMessage("c2", MsgQueueItem{LogID: "c2m2"}, r)

	cm.SetExpectedAck("c1", "ack-c1", r, 5*time.Second)
	cm.HandleAck("c1", "ack-c1", r)

	// Only c1m2 should be released; c2m2 stays queued.
	select {
	case got := <-r.ClientMsgChan:
		assert.True(t, strings.HasPrefix(got.LogID, "c1"),
			"expected c1-prefixed message, got %s", got.LogID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected c1m2 to release")
	}

	select {
	case got := <-r.ClientMsgChan:
		t.Fatalf("c2 should still be queued, got %s", got.LogID)
	case <-time.After(50 * time.Millisecond):
		// Good
	}

	// Now ack c2 and verify.
	cm.SetExpectedAck("c2", "ack-c2", r, 5*time.Second)
	cm.HandleAck("c2", "ack-c2", r)
	select {
	case got := <-r.ClientMsgChan:
		assert.True(t, strings.HasPrefix(got.LogID, "c2"),
			"expected c2-prefixed message, got %s", got.LogID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected c2m2 to release")
	}
}

func TestConvoManager_TimeoutTriggersImplicitAck(t *testing.T) {
	r, _ := newTestRouter(4)
	cm := NewConvoManager()

	cm.AddMessage("c1", MsgQueueItem{LogID: "m1", message: "first"}, r)
	<-r.ClientMsgChan
	cm.AddMessage("c1", MsgQueueItem{LogID: "m2", message: "second"}, r)
	cm.SetExpectedAck("c1", "ack1", r, 50*time.Millisecond)

	// Wait long enough for the timer to fire.
	require.Eventually(t, func() bool {
		select {
		case got := <-r.ClientMsgChan:
			return got.LogID == "m2"
		default:
			return false
		}
	}, 1*time.Second, 10*time.Millisecond, "expected m2 to release after timeout")
}

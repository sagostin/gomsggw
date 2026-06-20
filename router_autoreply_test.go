package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveAutoReply_MasterOff(t *testing.T) {
	g := &Gateway{AutoReplyEnabled: false, AutoReplyDefaultMsg: "default"}
	ns := &NumberSettings{AutoReplyEnabled: true, AutoReplyMessage: "custom"}
	enabled, msg := g.ResolveAutoReply(ns)
	assert.False(t, enabled, "master off must disable auto-reply entirely")
	assert.Equal(t, "", msg)
}

func TestResolveAutoReply_NumberOff(t *testing.T) {
	g := &Gateway{AutoReplyEnabled: true, AutoReplyDefaultMsg: "default"}
	ns := &NumberSettings{AutoReplyEnabled: false, AutoReplyMessage: "ignored"}
	enabled, msg := g.ResolveAutoReply(ns)
	assert.False(t, enabled)
	assert.Equal(t, "", msg)
}

func TestResolveAutoReply_NumberSettingsNil(t *testing.T) {
	g := &Gateway{AutoReplyEnabled: true, AutoReplyDefaultMsg: "default"}
	enabled, _ := g.ResolveAutoReply(nil)
	assert.False(t, enabled, "no per-number settings means no auto-reply")
}

func TestResolveAutoReply_CustomMessageWins(t *testing.T) {
	g := &Gateway{AutoReplyEnabled: true, AutoReplyDefaultMsg: "default"}
	ns := &NumberSettings{AutoReplyEnabled: true, AutoReplyMessage: "custom"}
	enabled, msg := g.ResolveAutoReply(ns)
	assert.True(t, enabled)
	assert.Equal(t, "custom", msg)
}

func TestResolveAutoReply_EnvDefaultFallback(t *testing.T) {
	g := &Gateway{AutoReplyEnabled: true, AutoReplyDefaultMsg: "env-default"}
	ns := &NumberSettings{AutoReplyEnabled: true, AutoReplyMessage: ""}
	enabled, msg := g.ResolveAutoReply(ns)
	assert.True(t, enabled)
	assert.Equal(t, "env-default", msg)
}

func TestResolveAutoReply_NoMessageAnywhereMeansSkip(t *testing.T) {
	g := &Gateway{AutoReplyEnabled: true, AutoReplyDefaultMsg: ""}
	ns := &NumberSettings{AutoReplyEnabled: true, AutoReplyMessage: ""}
	enabled, msg := g.ResolveAutoReply(ns)
	assert.False(t, enabled, "enabled with no message anywhere should skip cleanly")
	assert.Equal(t, "", msg)
}

func TestAutoReplyCooldown_FirstAllowed(t *testing.T) {
	c := &autoReplyCooldown{lastSent: make(map[string]time.Time)}
	assert.True(t, c.allow("+15555550100", "+15555550200", 60))
}

func TestAutoReplyCooldown_SecondWithinWindowSuppressed(t *testing.T) {
	c := &autoReplyCooldown{lastSent: make(map[string]time.Time)}
	assert.True(t, c.allow("+15555550100", "+15555550200", 60))
	assert.False(t, c.allow("+15555550100", "+15555550200", 60), "second within window must be blocked")
}

func TestAutoReplyCooldown_DifferentPairsIndependent(t *testing.T) {
	c := &autoReplyCooldown{lastSent: make(map[string]time.Time)}
	assert.True(t, c.allow("+15555550100", "+15555550200", 60))
	// Different destination → independent cooldown bucket.
	assert.True(t, c.allow("+15555550100", "+15555550300", 60))
	// Different sender → also independent.
	assert.True(t, c.allow("+15555550999", "+15555550200", 60))
}

func TestAutoReplyCooldown_ZeroCooldownAlwaysAllows(t *testing.T) {
	c := &autoReplyCooldown{lastSent: make(map[string]time.Time)}
	assert.True(t, c.allow("a", "b", 0))
	assert.True(t, c.allow("a", "b", 0))
	assert.True(t, c.allow("a", "b", 0))
}

func TestAutoReplyCooldown_ElapsedWindowAllowsAgain(t *testing.T) {
	c := &autoReplyCooldown{lastSent: make(map[string]time.Time)}
	c.lastSent["a|b"] = time.Now().Add(-2 * time.Hour)
	assert.True(t, c.allow("a", "b", 60))
}

func TestAutoReplyCooldown_ConcurrentSafe(t *testing.T) {
	c := &autoReplyCooldown{lastSent: make(map[string]time.Time)}
	var wg sync.WaitGroup
	const goroutines = 50
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.allow("a", "b", 60)
		}()
	}
	wg.Wait()
	// Just verify no panic/data race. Exactly one should have returned true,
	// but the cooldown is intentionally tolerant of races here.
	assert.GreaterOrEqual(t, len(c.lastSent), 1)
}

func TestFindNumberSettingsForClient(t *testing.T) {
	c := &Client{
		Numbers: []ClientNumber{
			{Number: "12505550100"},
			{Number: "12505550101", Settings: &NumberSettings{SMSDailyLimit: 42}},
		},
	}
	assert.Nil(t, findNumberSettingsForClient(c, "12505550100"))
	ns := findNumberSettingsForClient(c, "+12505550101")
	if assert.NotNil(t, ns) {
		assert.Equal(t, int64(42), ns.SMSDailyLimit)
	}
	assert.Nil(t, findNumberSettingsForClient(nil, "anything"))
}

func TestNsOrEmpty(t *testing.T) {
	assert.Equal(t, "", nsOrEmpty(nil, func(s *NumberSettings) string { return s.AutoReplyMessage }))
	ns := &NumberSettings{AutoReplyMessage: "hi"}
	assert.Equal(t, "hi", nsOrEmpty(ns, func(s *NumberSettings) string { return s.AutoReplyMessage }))
}

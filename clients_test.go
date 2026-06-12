package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldEnforceLimit(t *testing.T) {
	// Outbound is always enforced, regardless of limitBoth.
	assert.True(t, shouldEnforceLimit("outbound", false))
	assert.True(t, shouldEnforceLimit("outbound", true))

	// Inbound is only enforced when limitBoth=true.
	assert.False(t, shouldEnforceLimit("inbound", false))
	assert.True(t, shouldEnforceLimit("inbound", true))

	// Any other non-outbound direction follows the same rule: defer to limitBoth.
	assert.True(t, shouldEnforceLimit("weird", true))
	assert.False(t, shouldEnforceLimit("weird", false))
}

func TestGetEffectiveLimit_NumberOverridesClient(t *testing.T) {
	ns := &NumberSettings{
		SMSDailyLimit: 500,
	}
	cs := &ClientSettings{
		SMSDailyLimit: 1000,
	}

	limit, isNumberLevel, _ := getEffectiveLimit(cs, ns, "sms", "daily")
	assert.Equal(t, int64(500), limit, "number-level limit should win")
	assert.True(t, isNumberLevel)
}

func TestGetEffectiveLimit_FallsBackToClient(t *testing.T) {
	// Number has no limit set → client limit applies, level is client.
	limit, isNumberLevel, _ := getEffectiveLimit(
		&ClientSettings{SMSDailyLimit: 1000},
		&NumberSettings{SMSDailyLimit: 0},
		"sms", "daily",
	)
	assert.Equal(t, int64(1000), limit)
	assert.False(t, isNumberLevel)
}

func TestGetEffectiveLimit_UnlimitedWhenBothZero(t *testing.T) {
	limit, isNumberLevel, _ := getEffectiveLimit(
		&ClientSettings{SMSDailyLimit: 0},
		nil,
		"sms", "daily",
	)
	assert.Equal(t, int64(0), limit)
	assert.False(t, isNumberLevel)
}

func TestGetEffectiveLimit_PicksMessageTypeAndPeriod(t *testing.T) {
	cs := &ClientSettings{
		SMSBurstLimit:  10,
		SMSDailyLimit:  100,
		MMSBurstLimit:  20,
		MMSDailyLimit:  200,
	}
	ns := &NumberSettings{
		SMSMonthlyLimit: 50, // only this one is set on the number
	}

	// SMS burst falls through to client (number has no burst)
	l, level, _ := getEffectiveLimit(cs, ns, "sms", "burst")
	assert.Equal(t, int64(10), l)
	assert.False(t, level)

	// SMS monthly picks the number-level
	l, level, _ = getEffectiveLimit(cs, ns, "sms", "monthly")
	assert.Equal(t, int64(50), l)
	assert.True(t, level)

	// MMS daily picks the client-level (number has no daily mms)
	l, level, _ = getEffectiveLimit(cs, ns, "mms", "daily")
	assert.Equal(t, int64(200), l)
	assert.False(t, level)
}

func TestGetEffectiveLimit_LimitBothPropagates(t *testing.T) {
	// Number has its own limit and its own limit_both=true
	ns := &NumberSettings{SMSDailyLimit: 100, LimitBoth: true}
	cs := &ClientSettings{SMSDailyLimit: 1000, LimitBoth: false}

	_, _, both := getEffectiveLimit(cs, ns, "sms", "daily")
	assert.True(t, both, "number-level limit_both should propagate")

	// When falling back to client, client's limit_both should propagate.
	ns = &NumberSettings{SMSDailyLimit: 0, LimitBoth: false}
	_, _, both = getEffectiveLimit(cs, ns, "sms", "daily")
	assert.False(t, both)
}

func TestGetPeriodStart_Burst(t *testing.T) {
	// Burst is "the last minute" — should be ~now-1min, regardless of timezone.
	now := time.Now()
	c := &Client{Timezone: "America/Vancouver"}

	start := GetPeriodStart(c, "burst")
	delta := now.Sub(start)
	assert.True(t, delta >= 50*time.Second && delta <= 70*time.Second,
		"burst should be ~1 minute ago, got %v", delta)
}

func TestGetPeriodStart_DailyRespectsTimezone(t *testing.T) {
	c := &Client{Timezone: "America/Vancouver"}

	got := GetPeriodStart(c, "daily")

	// Midnight in the client's timezone today, expressed in UTC.
	loc, err := time.LoadLocation("America/Vancouver")
	require.NoError(t, err)
	now := time.Now().In(loc)
	want := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()
	assert.Equal(t, want, got)
}

func TestGetPeriodStart_DailyUTCDefault(t *testing.T) {
	// No timezone set → falls back to UTC
	c := &Client{}
	got := GetPeriodStart(c, "daily")

	// Result should be a midnight-UTC instant today
	utcNow := time.Now().UTC()
	assert.Equal(t, utcNow.Year(), got.Year())
	assert.Equal(t, utcNow.Month(), got.Month())
	assert.Equal(t, utcNow.Day(), got.Day())
	assert.Equal(t, 0, got.Hour())
	assert.Equal(t, 0, got.Minute())
	assert.Equal(t, 0, got.Second())
}

func TestGetPeriodStart_MonthlyRespectsTimezone(t *testing.T) {
	c := &Client{Timezone: "America/Vancouver"}
	got := GetPeriodStart(c, "monthly")

	loc, err := time.LoadLocation("America/Vancouver")
	require.NoError(t, err)
	now := time.Now().In(loc)
	want := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).UTC()
	assert.Equal(t, want, got)
}

func TestGetPeriodStart_UnknownPeriodDefaultsTo24h(t *testing.T) {
	c := &Client{}
	start := GetPeriodStart(c, "unknown")
	delta := time.Since(start)
	assert.True(t, delta > 23*time.Hour && delta < 25*time.Hour,
		"unknown period should default to ~24h ago, got %v", delta)
}

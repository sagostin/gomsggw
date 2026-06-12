package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitSMS_EmptyInput(t *testing.T) {
	assert.Empty(t, splitSMS("", 10))
}

func TestSplitSMS_UnderLimit(t *testing.T) {
	// 5 bytes, limit 10 → stays as one segment.
	got := splitSMS("hello", 10)
	assert.Equal(t, []string{"hello"}, got)
}

func TestSplitSMS_ExactlyAtLimit(t *testing.T) {
	// 5 bytes, limit 5 → fits in one segment (the `>` check is strict).
	got := splitSMS("hello", 5)
	assert.Equal(t, []string{"hello"}, got)
}

func TestSplitSMS_OverLimitSplitsCleanly(t *testing.T) {
	// 10 bytes, limit 3 → segments of 3, 3, 3, 1.
	got := splitSMS("abcdefghij", 3)
	assert.Equal(t, []string{"abc", "def", "ghi", "j"}, got)
}

func TestSplitSMS_SingleRuneExceedingLimit(t *testing.T) {
	// Each '€' is 3 bytes. The split is strict (uses `>`): a rune whose bytes
	// would equal the limit triggers a flush and starts a new segment.
	threeEuros := "€€€"

	// limit=4: first € fits (3 ≤ 4), second would push to 6 > 4 → flush.
	// So segments are [€][€][€].
	assert.Equal(t, []string{"€", "€", "€"}, splitSMS(threeEuros, 4))

	// limit=9: first + second + third = 9, the third comparison is 6+3=9 NOT > 9,
	// so the third € gets appended. Result: one segment.
	assert.Equal(t, []string{"€€€"}, splitSMS(threeEuros, 9))
}

func TestSplitSMS_AsciiAtExactByteBoundary(t *testing.T) {
	// Sanity: a 4-byte ASCII string at limit 2 → 2 + 2.
	got := splitSMS("abcd", 2)
	assert.Equal(t, []string{"ab", "cd"}, got)
}

func TestSplitSMS_PreservesOrder(t *testing.T) {
	// 20-byte input, limit 7 → 7, 7, 6.
	got := splitSMS(strings.Repeat("a", 20), 7)
	assert.Equal(t, []string{strings.Repeat("a", 7), strings.Repeat("a", 7), strings.Repeat("a", 6)}, got)
}

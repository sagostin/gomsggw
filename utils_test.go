package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringInArray(t *testing.T) {
	assert.True(t, StringInArray("b", []string{"a", "b", "c"}))
	assert.False(t, StringInArray("d", []string{"a", "b", "c"}))
	assert.False(t, StringInArray("", []string{"a", "b"}))
	assert.False(t, StringInArray("a", nil))
	assert.False(t, StringInArray("a", []string{}))
}

func TestSafeClientUsername(t *testing.T) {
	assert.Equal(t, "", safeClientUsername(nil))
	c := &Client{Username: "alice"}
	assert.Equal(t, "alice", safeClientUsername(c))
}

func TestGetSMSEncoding_GSM7(t *testing.T) {
	// Pure ASCII letters → GSM-7.
	assert.Equal(t, "gsm7", GetSMSEncoding("Hello, world!"))
}

func TestGetSMSEncoding_UCS2(t *testing.T) {
	// Non-GSM characters (e.g. emoji, CJK) → UCS-2.
	assert.Equal(t, "ucs2", GetSMSEncoding("你好世界"))
	assert.Equal(t, "ucs2", GetSMSEncoding("Hello 👋"))
}

func TestGetSMSSegmentCount_Empty(t *testing.T) {
	assert.Equal(t, 0, GetSMSSegmentCount(""))
}

func TestGetSMSSegmentCount_ShortMessageIsOneSegment(t *testing.T) {
	assert.Equal(t, 1, GetSMSSegmentCount("Hello"))
}

func TestGetSMSSegmentCount_LongMessageIsMultiple(t *testing.T) {
	// 200 ASCII chars should exceed a single GSM-7 segment (160 chars).
	count := GetSMSSegmentCount(stringRepeat("a", 200))
	assert.GreaterOrEqual(t, count, 2, "200-char ASCII should be ≥ 2 GSM-7 segments")
}

// stringRepeat avoids pulling in strings just for this test file.
func stringRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

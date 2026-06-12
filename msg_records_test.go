package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPartiallyRedactMessage_ShortMessageFullyRedacted(t *testing.T) {
	// Messages ≤ 10 chars are completely redacted.
	assert.Equal(t, "**********", PartiallyRedactMessage(""))
	assert.Equal(t, "**********", PartiallyRedactMessage("hi"))
	assert.Equal(t, "**********", PartiallyRedactMessage("1234567890"))
}

func TestPartiallyRedactMessage_LongMessageKeepsFirstFive(t *testing.T) {
	got := PartiallyRedactMessage("Hello, sensitive world!")
	// First 5 chars + literal "*****"
	assert.Equal(t, "Hello*****", got)
}

func TestPartiallyRedactMessage_ExactlyElevenChars(t *testing.T) {
	// 11 chars is > 10 → partial redaction path.
	assert.Equal(t, "Hello*****", PartiallyRedactMessage("Hello-world"))
}

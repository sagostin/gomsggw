package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateAndCleanSMS_PassesBasicCharacters(t *testing.T) {
	in := "Hello, world! 123 @#$"
	assert.Equal(t, in, ValidateAndCleanSMS(in))
}

func TestValidateAndCleanSMS_ReplacesDisallowed(t *testing.T) {
	// '日' is not in the basic or extended set → replaced with '?'.
	got := ValidateAndCleanSMS("a日b")
	assert.Equal(t, "a?b", got)
}

func TestValidateAndCleanSMS_EscapesExtendedCharacters(t *testing.T) {
	// Extended chars (e.g. '^', '€') are preceded by ESC (\x1B) in the output.
	for _, ch := range []string{"^", "{", "}", "\\", "[", "~", "]", "|", "€"} {
		t.Run(ch, func(t *testing.T) {
			got := ValidateAndCleanSMS(ch)
			// Count runes, not bytes — multi-byte chars (€) are 1 rune but 3 bytes.
			assert.Equal(t, 2, len([]rune(got)),
				"extended char should be ESC + char (2 runes)")
			assert.Equal(t, byte(0x1B), got[0])
			assert.Equal(t, ch, string([]rune(got)[1:]))
		})
	}
}

func TestValidateAndCleanSMS_MixedInput(t *testing.T) {
	// "Hello ^world" → "Hello " + ESC + "^" + "world"
	got := ValidateAndCleanSMS("Hello ^world")
	assert.Equal(t, "Hello \x1B^world", got)
}

func TestValidateAndCleanSMS_EmptyString(t *testing.T) {
	assert.Equal(t, "", ValidateAndCleanSMS(""))
}

func TestValidateAndCleanSMS_AllDisallowedBecomesAllQuestionMarks(t *testing.T) {
	got := ValidateAndCleanSMS("日中文")
	assert.Equal(t, "???", got)
}

func TestValidateAndCleanSMS_TreatsBasicAndExtendedConsistently(t *testing.T) {
	// A character that's basic must NOT be escaped.
	basic := ValidateAndCleanSMS("A")
	assert.Equal(t, "A", basic)
	assert.False(t, strings.HasPrefix(basic, "\x1B"))
}

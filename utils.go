package main

import (
	"os"
	"strconv"
	"strings"

	"zultys-smpp-mm4/smpp/coding"
)

// envIntOrDefault reads an int from the environment, falling back to def when
// unset or unparsable.
func envIntOrDefault(key string, def int) int {
	if val := os.Getenv(key); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			return v
		}
	}
	return def
}

// parseSMTPEncryption resolves the SMTP transport encryption mode.
// SMTP_TLS_MODE takes precedence: "starttls" (default), "tls" (implicit TLS),
// or "none" (plaintext — for trusted internal relays). Falls back to the
// legacy SMTP_STARTTLS flag: true/unset → starttls, false → tls.
func parseSMTPEncryption() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS_MODE"))) {
	case "starttls":
		return "starttls"
	case "tls":
		return "tls"
	case "none":
		return "none"
	}
	if val := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_STARTTLS"))); val == "false" || val == "0" {
		return "tls"
	}
	return "starttls"
}

// StringInArray checks if a string exists in an array of strings
func StringInArray(target string, list []string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// safeClientUsername helper so we don't nil-deref
func safeClientUsername(c *Client) string {
	if c == nil {
		return ""
	}
	return c.Username
}

// nsOrEmpty safely dereferences an optional NumberSettings pointer and returns
// a value via the accessor; returns "" when ns is nil.
func nsOrEmpty(ns *NumberSettings, accessor func(*NumberSettings) string) string {
	if ns == nil {
		return ""
	}
	return accessor(ns)
}

// effectiveEmailMode returns the stored email-delivery mode, defaulting to
// "both" when unset or invalid.
func effectiveEmailMode(ns *NumberSettings) string {
	if ns == nil || ns.EmailDeliveryMode == "" {
		return EmailDeliveryModeBoth
	}
	return ns.EmailDeliveryMode
}

// GetSMSEncoding determines the encoding type for an SMS message
// Returns "gsm7", "ucs2", or "ascii" based on the message content
func GetSMSEncoding(message string) string {
	bestCoding := coding.BestSafeCoding(message)
	switch bestCoding {
	case coding.GSM7BitCoding:
		return "gsm7"
	case coding.UCS2Coding:
		return "ucs2"
	case coding.ASCIICoding:
		return "ascii"
	case coding.Latin1Coding:
		return "latin1"
	default:
		return "gsm7"
	}
}

// GetSMSSegmentCount calculates the number of SMS segments needed for a message
func GetSMSSegmentCount(message string) int {
	if message == "" {
		return 0
	}
	bestCoding := coding.BestSafeCoding(message)
	segments := coding.SplitSMS(message, byte(bestCoding))
	return len(segments)
}

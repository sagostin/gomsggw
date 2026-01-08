package main

import (
	"zultys-smpp-mm4/smpp/coding"
)

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

package main

import (
	"strings"
)

// Define the GSM 03.38 basic and extended character sets
var gsm0338BasicSet = map[rune]bool{
	'@': true, '£': true, '$': true, '¥': true, 'è': true, 'é': true,
	'ù': true, 'ì': true, 'ò': true, 'Ç': true, '\n': true, 'Ø': true,
	'ø': true, 'Å': true, 'Δ': true, '_': true, 'Φ': true, 'Γ': true,
	'Λ': true, 'Ω': true, 'Π': true, 'Ψ': true, 'Σ': true, 'Θ': true,
	'Ξ': true, 'Æ': true, 'æ': true, 'ß': true, 'É': true,
	' ': true, '!': true, '"': true, '#': true, '¤': true, '%': true,
	'&': true, '\'': true, '(': true, ')': true, '*': true, '+': true,
	',': true, '-': true, '.': true, '/': true, '0': true, '1': true,
	'2': true, '3': true, '4': true, '5': true, '6': true, '7': true,
	'8': true, '9': true, ':': true, ';': true, '<': true, '=': true,
	'>': true, '?': true, '¡': true, 'A': true, 'B': true, 'C': true,
	'D': true, 'E': true, 'F': true, 'G': true, 'H': true, 'I': true,
	'J': true, 'K': true, 'L': true, 'M': true, 'N': true, 'O': true,
	'P': true, 'Q': true, 'R': true, 'S': true, 'T': true, 'U': true,
	'V': true, 'W': true, 'X': true, 'Y': true, 'Z': true, 'Ä': true,
	'Ö': true, 'Ñ': true, 'Ü': true, '§': true, '¿': true, 'a': true,
	'b': true, 'c': true, 'd': true, 'e': true, 'f': true, 'g': true,
	'h': true, 'i': true, 'j': true, 'k': true, 'l': true, 'm': true,
	'n': true, 'o': true, 'p': true, 'q': true, 'r': true, 's': true,
	't': true, 'u': true, 'v': true, 'w': true, 'x': true, 'y': true,
	'z': true, 'ä': true, 'ö': true, 'ñ': true, 'ü': true, 'à': true,
}

// Define the GSM 03.38 extended character set
var gsm0338ExtendedSet = map[rune]bool{
	'^': true, '{': true, '}': true, '\\': true, '[': true, '~': true,
	']': true, '|': true, '€': true,
}

// ValidateAndCleanSMS validates and cleans a string based on GSM 03.38 allowed characters.
// It replaces disallowed characters with '?' and handles extended characters with escape sequences.
func ValidateAndCleanSMS(text string) string {
	var builder strings.Builder

	for _, r := range text {
		if gsm0338BasicSet[r] {
			// Allowed basic character, append as is
			builder.WriteRune(r)
		} else if gsm0338ExtendedSet[r] {
			// Allowed extended character, append escape character followed by the actual character
			builder.WriteRune('\x1B') // ESC character
			builder.WriteRune(r)
		} else {
			// Disallowed character, replace with '?'
			builder.WriteRune('?')
		}
	}

	return builder.String()
}

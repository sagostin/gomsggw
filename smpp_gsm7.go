package main

import (
	"errors"
	"fmt"
)

// --- Reverse Mapping Tables ---
// gsm7ReverseMap maps GSM‑7 default table codes (0x00–0x7F) to runes.
var gsm7ReverseMap = map[byte]rune{
	0x00: '@',
	0x01: '£',
	0x02: '$',
	0x03: '¥',
	0x04: 'è',
	0x05: 'é',
	0x06: 'ù',
	0x07: 'ì',
	0x08: 'ò',
	0x09: 'Ç',
	0x0A: '\n', // line feed
	0x0B: 'Ø',
	0x0C: 'ø',
	0x0D: '\r', // carriage return
	0x0E: 'Å',
	0x0F: 'å',
	0x10: 'Δ',
	0x11: '_',
	0x12: 'Φ',
	0x13: 'Γ',
	0x14: 'Λ',
	0x15: 'Ω',
	0x16: 'Π',
	0x17: 'Ψ',
	0x18: 'Σ',
	0x19: 'Θ',
	0x1A: 'Ξ',
	// 0x1B is reserved for escape (handled separately)
	0x1C: 'Æ',
	0x1D: 'æ',
	0x1E: 'ß',
	0x1F: 'É',
	0x20: ' ',
	0x21: '!',
	0x22: '"',
	0x23: '#',
	0x24: '$',
	0x25: '%',
	0x26: '&',
	0x27: '\'',
	0x28: '(',
	0x29: ')',
	0x2A: '*',
	0x2B: '+',
	0x2C: ',',
	0x2D: '-',
	0x2E: '.',
	0x2F: '/',
	0x30: '0',
	0x31: '1',
	0x32: '2',
	0x33: '3',
	0x34: '4',
	0x35: '5',
	0x36: '6',
	0x37: '7',
	0x38: '8',
	0x39: '9',
	0x3A: ':',
	0x3B: ';',
	0x3C: '<',
	0x3D: '=',
	0x3E: '>',
	0x3F: '?',
	0x40: '¡',
	0x41: 'A',
	0x42: 'B',
	0x43: 'C',
	0x44: 'D',
	0x45: 'E',
	0x46: 'F',
	0x47: 'G',
	0x48: 'H',
	0x49: 'I',
	0x4A: 'J',
	0x4B: 'K',
	0x4C: 'L',
	0x4D: 'M',
	0x4E: 'N',
	0x4F: 'O',
	0x50: 'P',
	0x51: 'Q',
	0x52: 'R',
	0x53: 'S',
	0x54: 'T',
	0x55: 'U',
	0x56: 'V',
	0x57: 'W',
	0x58: 'X',
	0x59: 'Y',
	0x5A: 'Z',
	0x5B: 'Ä',
	0x5C: 'Ö',
	0x5D: 'Ñ',
	0x5E: 'Ü',
	0x5F: '§',
	0x60: '¿',
	0x61: 'a',
	0x62: 'b',
	0x63: 'c',
	0x64: 'd',
	0x65: 'e',
	0x66: 'f',
	0x67: 'g',
	0x68: 'h',
	0x69: 'i',
	0x6A: 'j',
	0x6B: 'k',
	0x6C: 'l',
	0x6D: 'm',
	0x6E: 'n',
	0x6F: 'o',
	0x70: 'p',
	0x71: 'q',
	0x72: 'r',
	0x73: 's',
	0x74: 't',
	0x75: 'u',
	0x76: 'v',
	0x77: 'w',
	0x78: 'x',
	0x79: 'y',
	0x7A: 'z',
	0x7B: 'ä',
	0x7C: 'ö',
	0x7D: 'ñ',
	0x7E: 'ü',
	0x7F: 'à',
}

// gsm7ExtReverseMap maps extension codes (following 0x1B) to runes.
var gsm7ExtReverseMap = map[byte]rune{
	0x0A: '\f', // sometimes used for form feed
	0x14: '^',
	0x28: '{',
	0x29: '}',
	0x2F: '\\',
	0x3C: '[',
	0x3D: '~',
	0x3E: ']',
	0x40: '|',
	0x65: '€',
}

// --- Decoding Functions ---

// decodeUnpackedGSM7 decodes a GSM‑7 "unpacked" byte slice into a string.
// In this mode, each byte represents a 7‑bit value. An escape byte (0x1B)
// indicates that the following byte is from the extension table.
func decodeUnpackedGSM7(input []byte) (string, error) {
	var result []rune
	for i := 0; i < len(input); i++ {
		b := input[i]
		if b == 0x1B {
			// Escape character – next byte should be in extension table.
			if i+1 >= len(input) {
				return "", errors.New("invalid GSM7 encoding: escape at end of input")
			}
			i++
			extByte := input[i]
			if r, ok := gsm7ExtReverseMap[extByte]; ok {
				result = append(result, r)
			} else {
				return "", fmt.Errorf("invalid GSM7 extension code: 0x%X", extByte)
			}
		} else {
			if r, ok := gsm7ReverseMap[b]; ok {
				result = append(result, r)
			} else {
				return "", fmt.Errorf("invalid GSM7 byte: 0x%X", b)
			}
		}
	}
	return string(result), nil
}

// unpackSeptets takes a packed GSM‑7 byte slice and unpacks it into septets
// (each representing a 7‑bit value). This follows the GSM‑7 packing algorithm.
func unpackSeptets(packed []byte) ([]byte, error) {
	var septets []byte
	var carry uint8 = 0
	var carryBits uint = 0

	for i := 0; i < len(packed); i++ {
		b := packed[i]
		// Combine any carried bits with the current byte.
		septet := (b << carryBits) | carry
		septets = append(septets, septet&0x7F)
		// Prepare new carry from the high bits of b.
		carry = b >> (7 - carryBits)
		carryBits++
		if carryBits == 7 {
			// When 7 bits have been carried over, that forms a full septet.
			septets = append(septets, carry&0x7F)
			carry = 0
			carryBits = 0
		}
	}
	// If there are leftover bits, append them as a septet.
	if carryBits > 0 {
		septets = append(septets, carry&0x7F)
	}
	return septets, nil
}

// decodePackedGSM7 decodes a GSM‑7 "packed" byte slice into a string.
// It first unpacks the septets from the packed format, then decodes them.
func decodePackedGSM7(input []byte) (string, error) {
	septets, err := unpackSeptets(input)
	if err != nil {
		return "", err
	}
	return decodeUnpackedGSM7(septets)
}

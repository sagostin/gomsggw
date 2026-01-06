package pdu

import (
	"errors"
)

//goland:noinspection ALL
var (
	ErrUnmarshalPDUFailed   = errors.New("pdu: unmarshal pdu failed")
	ErrUnknownDataCoding    = errors.New("pdu: unknown data coding")
	ErrInvalidSequence      = errors.New("pdu: invalid sequence (should be 31 bit integer)")
	ErrItemTooMany          = errors.New("pdu: item too many")
	ErrDataTooLarge         = errors.New("pdu: data too large")
	ErrUnparseableTime      = errors.New("pdu: unparseable time")
	ErrShortMessageTooLarge = errors.New("pdu: encoded short message data exceeds size of 140 bytes")
	ErrMultipartTooMuch     = errors.New("pdu: multipart sms too much (max 254 segments)")
)

const (
	ErrInvalidCommandLength CommandStatus = 0x002
	ErrInvalidCommandID     CommandStatus = 0x003
	ErrInvalidDestCount     CommandStatus = 0x033
	ErrInvalidDestFlag      CommandStatus = 0x040
	ErrInvalidTagLength     CommandStatus = 0x0C2
	ErrUnknownError         CommandStatus = 0x0FF
	ErrInvalidSystemID      CommandStatus = 0x0000000F // ESME_RINVSYSID
	ErrInvalidPasswd        CommandStatus = 0x0000000E // ESME_RINVPASWD
	ErrBindFail             CommandStatus = 0x00000005 // ESME_RBINDFAIL
	ESME_ROK                CommandStatus = 0x00000000
)

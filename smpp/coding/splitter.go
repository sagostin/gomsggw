package coding

type Splitter func(rune) int

var (
	_7BitSplitter      Splitter = func(rune) int { return 7 }
	_1ByteSplitter     Splitter = func(rune) int { return 8 }
	_MultibyteSplitter Splitter = func(r rune) int {
		if r < 0x7F {
			return 8
		}
		return 16
	}
	_UTF16Splitter Splitter = func(r rune) int {
		if (r <= 0xD7FF) || ((r >= 0xE000) && (r <= 0xFFFF)) {
			return 16
		}
		return 32
	}
)

func (fn Splitter) Len(input string) (n int) {
	for _, point := range input {
		n += fn(point)
	}
	if n%8 != 0 {
		n += 8 - n%8
	}
	return n / 8
}

func (fn Splitter) Split(input string, limit int) (segments []string) {
	limit *= 8
	points := []rune(input)
	var start, length int
	for i := 0; i < len(points); i++ {
		length += fn(points[i])
		if length > limit {
			segments = append(segments, string(points[start:i]))
			start, length = i, 0
			i--
		}
	}
	if length > 0 {
		segments = append(segments, string(points[start:]))
	}
	return
}

// SplitSMS will split `msg` into the correct single- or multipart segments
// based on SMS rules and the given DataCoding value.
func SplitSMS(msg string, dataCoding byte) []string {
	// pick your splitter
	var sp Splitter
	switch dataCoding {
	case 8: // UCS-2/UTF-16
		sp = _UTF16Splitter
	case 1: // ASCII / 1-byte
		sp = _1ByteSplitter
	default: // GSM-7
		sp = _7BitSplitter
	}

	const (
		singleLimitBytes    = 140     // 1120 bits
		multipartLimitBytes = 140 - 6 // 6-byte UDH overhead → 134 bytes
	)

	// if it fits in one SMS…
	if sp.Len(msg) <= singleLimitBytes {
		return []string{msg}
	}
	// otherwise chop into 134-byte pieces
	return sp.Split(msg, multipartLimitBytes)
}

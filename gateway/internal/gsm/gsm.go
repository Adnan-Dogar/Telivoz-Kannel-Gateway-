// Package gsm handles SMS text encoding: GSM 03.38 7-bit (unpacked, as used over SMPP), UCS-2, and
// splitting long messages into concatenated parts.
package gsm

import (
	"strings"
	"unicode/utf16"
)

const (
	CodingDefault byte = 0 // GSM 7-bit default alphabet
	CodingLatin1  byte = 3
	CodingUCS2    byte = 8
)

const basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

var (
	toGSM   = map[rune]byte{}
	fromGSM [128]rune
	toExt   = map[rune]byte{'\f': 0x0A, '^': 0x14, '{': 0x28, '}': 0x29, '\\': 0x2F, '[': 0x3C, '~': 0x3D, ']': 0x3E, '|': 0x40, '€': 0x65}
	fromExt = map[byte]rune{}
)

func init() {
	i := 0
	for _, r := range basic {
		fromGSM[i] = r
		if r != 0x1b {
			toGSM[r] = byte(i)
		}
		i++
	}
	for r, b := range toExt {
		fromExt[b] = r
	}
}

// IsGSM reports whether every character fits the GSM 7-bit alphabet (with extension table).
func IsGSM(s string) bool {
	for _, r := range s {
		if _, ok := toGSM[r]; ok {
			continue
		}
		if _, ok := toExt[r]; ok {
			continue
		}
		return false
	}
	return true
}

// EncodeGSM returns unpacked GSM septets (one per byte); extension characters take two.
// Characters outside the alphabet become '?'.
func EncodeGSM(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if b, ok := toGSM[r]; ok {
			out = append(out, b)
		} else if b, ok := toExt[r]; ok {
			out = append(out, 0x1b, b)
		} else {
			out = append(out, toGSM['?'])
		}
	}
	return out
}

// DecodeGSM converts unpacked GSM septets to text.
func DecodeGSM(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i++ {
		c := b[i] & 0x7f
		if c == 0x1b && i+1 < len(b) {
			if r, ok := fromExt[b[i+1]&0x7f]; ok {
				sb.WriteRune(r)
				i++
				continue
			}
		}
		sb.WriteRune(fromGSM[c])
	}
	return sb.String()
}

// EncodeUCS2 returns UTF-16 big-endian bytes.
func EncodeUCS2(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u))
	}
	return out
}

// DecodeUCS2 converts UTF-16 big-endian bytes to text.
func DecodeUCS2(b []byte) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return string(utf16.Decode(units))
}

// Decode turns an SMPP short_message into text. When udhi is set, the user data header is removed and
// returned separately.
func Decode(dataCoding byte, payload []byte, udhi bool) (text string, udh []byte) {
	if udhi && len(payload) > 0 && int(payload[0])+1 <= len(payload) {
		n := int(payload[0]) + 1
		udh, payload = payload[:n], payload[n:]
	}
	switch dataCoding {
	case CodingUCS2:
		return DecodeUCS2(payload), udh
	case CodingDefault:
		return DecodeGSM(payload), udh
	default: // IA5/ASCII, Latin-1 and anything else: byte per character
		r := make([]rune, len(payload))
		for i, c := range payload {
			r[i] = rune(c)
		}
		return string(r), udh
	}
}

// Split encodes text and splits it into SMS parts. One part means no UDH is needed; several parts must be
// sent with a concatenation UDH (see ConcatUDH).
func Split(text string) (coding byte, parts [][]byte) {
	if IsGSM(text) {
		enc := EncodeGSM(text)
		if len(enc) <= 160 {
			return CodingDefault, [][]byte{enc}
		}
		for len(enc) > 0 {
			n := 153
			if n >= len(enc) {
				n = len(enc)
			} else if enc[n-1] == 0x1b { // never split an escape sequence
				n--
			}
			parts = append(parts, enc[:n])
			enc = enc[n:]
		}
		return CodingDefault, parts
	}
	enc := EncodeUCS2(text)
	if len(enc) <= 140 {
		return CodingUCS2, [][]byte{enc}
	}
	for len(enc) > 0 {
		n := 134
		if n >= len(enc) {
			n = len(enc)
		} else if hi := uint16(enc[n-2])<<8 | uint16(enc[n-1]); hi >= 0xD800 && hi <= 0xDBFF { // keep surrogate pairs together
			n -= 2
		}
		parts = append(parts, enc[:n])
		enc = enc[n:]
	}
	return CodingUCS2, parts
}

// Parts returns how many SMS parts the text needs.
func Parts(text string) int {
	_, p := Split(text)
	return len(p)
}

// ConcatUDH builds the 8-bit-reference concatenation header for part seq (1-based) of total.
func ConcatUDH(ref byte, total, seq int) []byte {
	return []byte{0x05, 0x00, 0x03, ref, byte(total), byte(seq)}
}

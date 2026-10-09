package gsm

import (
	"strings"
	"testing"
)

func TestGSMRoundTrip(t *testing.T) {
	s := "Hello @ £5 {ok} €"
	if !IsGSM(s) {
		t.Fatal("expected GSM text")
	}
	if got := DecodeGSM(EncodeGSM(s)); got != s {
		t.Fatalf("round trip: %q", got)
	}
	if len(EncodeGSM("€")) != 2 {
		t.Fatal("extension char must take two septets")
	}
}

func TestUnicodeDetectedAndRoundTrips(t *testing.T) {
	s := "سلام 😀"
	if IsGSM(s) {
		t.Fatal("expected non-GSM text")
	}
	if got := DecodeUCS2(EncodeUCS2(s)); got != s {
		t.Fatalf("round trip: %q", got)
	}
}

func TestSplitLimits(t *testing.T) {
	cases := []struct {
		text  string
		coding byte
		parts int
	}{
		{strings.Repeat("a", 160), CodingDefault, 1},
		{strings.Repeat("a", 161), CodingDefault, 2},
		{strings.Repeat("a", 306), CodingDefault, 2},
		{strings.Repeat("a", 307), CodingDefault, 3},
		{strings.Repeat("ب", 70), CodingUCS2, 1},
		{strings.Repeat("ب", 71), CodingUCS2, 2},
		{strings.Repeat("ب", 134), CodingUCS2, 2},
	}
	for _, c := range cases {
		coding, parts := Split(c.text)
		if coding != c.coding || len(parts) != c.parts {
			t.Errorf("%d chars: got coding %d parts %d, want %d/%d", len([]rune(c.text)), coding, len(parts), c.coding, c.parts)
		}
	}
}

func TestSplitNeverBreaksEscapeOrSurrogate(t *testing.T) {
	_, parts := Split(strings.Repeat("a", 152) + "€" + strings.Repeat("a", 10))
	if parts[0][len(parts[0])-1] == 0x1b {
		t.Fatal("escape split across parts")
	}
	_, parts = Split(strings.Repeat("ب", 66) + "😀" + "ب")
	for _, p := range parts {
		if DecodeUCS2(p) == "" || strings.ContainsRune(DecodeUCS2(p), '�') {
			t.Fatal("surrogate pair split across parts")
		}
	}
}

func TestDecodeStripsUDH(t *testing.T) {
	payload := append(ConcatUDH(7, 2, 1), EncodeGSM("hi")...)
	text, udh := Decode(CodingDefault, payload, true)
	if text != "hi" || len(udh) != 6 {
		t.Fatalf("got %q udh %v", text, udh)
	}
}

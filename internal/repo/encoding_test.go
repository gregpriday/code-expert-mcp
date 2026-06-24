package repo

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

func TestNormalizeToUTF8(t *testing.T) {
	// Valid UTF-8 (including multibyte) passes through byte-identical.
	in := []byte("hello, 世界 café\n")
	if got := normalizeToUTF8(in); !bytes.Equal(got, in) {
		t.Errorf("valid UTF-8 must pass through unchanged, got %q", got)
	}

	// Empty input is unchanged.
	if got := normalizeToUTF8(nil); len(got) != 0 {
		t.Errorf("empty input should stay empty, got %q", got)
	}

	// A UTF-8 BOM is stripped.
	bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte("abc")...)
	if got := normalizeToUTF8(bom); !bytes.Equal(got, []byte("abc")) {
		t.Errorf("UTF-8 BOM not stripped: %q", got)
	}

	// Windows-1252 bytes decode to UTF-8: 0xE9->é, 0x92->’, 0x97->—, 0x80->€.
	win := []byte{'c', 'a', 'f', 0xE9, ' ', 0x92, ' ', 0x97, ' ', 0x80}
	got := normalizeToUTF8(win)
	if !utf8.Valid(got) {
		t.Fatalf("result must be valid UTF-8, got %x", got)
	}
	for _, want := range []string{"café", "’", "—", "€"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("Windows-1252 decode missing %q in %q", want, got)
		}
	}

	// Latin-1 high bytes (0xA0–0xFF) decode to their code point.
	if got := normalizeToUTF8([]byte{'a', 0xF1, 'o'}); !bytes.Contains(got, []byte("ñ")) {
		t.Errorf("0xF1 should decode to ñ, got %q", got)
	}
}

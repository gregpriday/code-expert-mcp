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

	// A mostly-UTF-8 file with one stray legacy byte must keep its valid UTF-8
	// runs intact and only remap the stray byte (not transcode the whole file).
	mixed := []byte("café ")    // valid UTF-8 (é = c3 a9)
	mixed = append(mixed, 0x92) // a stray Windows-1252 right single quote
	mixed = append(mixed, []byte(" déjà vu")...)
	gotMixed := normalizeToUTF8(mixed)
	if !utf8.Valid(gotMixed) {
		t.Fatalf("mixed result must be valid UTF-8, got %x", gotMixed)
	}
	for _, want := range []string{"café", "déjà", "’"} {
		if !bytes.Contains(gotMixed, []byte(want)) {
			t.Errorf("mixed decode lost or corrupted %q: got %q", want, gotMixed)
		}
	}
	// The valid multibyte runes must not have been re-decoded into mojibake.
	if bytes.Contains(gotMixed, []byte("Ã©")) || bytes.Contains(gotMixed, []byte("Ã ")) {
		t.Errorf("mixed decode corrupted valid UTF-8 into Windows-1252 mojibake: %q", gotMixed)
	}
}

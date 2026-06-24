package repo

import (
	"strings"
	"unicode/utf8"
)

// normalizeToUTF8 returns data as valid UTF-8 so that search, reads, and the
// trigram index all operate on one consistent byte space across any source
// language. Valid UTF-8 (the overwhelming common case) and empty input are
// returned unchanged, so nearly every file is byte-identical and behaviour is
// unaffected. A UTF-8 BOM is stripped. Anything else is decoded as Windows-1252
// (a superset of Latin-1), which deterministically recovers legacy single-byte
// text — common in older PHP, JS, and CSS — without any external dependency.
//
// Binary files are detected and skipped upstream, so this only ever sees text;
// UTF-16 sources carry interleaved NUL bytes and are treated as binary there.
func normalizeToUTF8(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}
	if utf8.Valid(data) {
		return data
	}
	return decodeWindows1252(data)
}

// win1252High maps the 0x80–0x9F bytes that Windows-1252 assigns to printable
// characters (the range where it diverges from Latin-1). Bytes in 0xA0–0xFF, and
// the few undefined slots, decode as their Latin-1 code point.
var win1252High = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„',
	0x85: '…', 0x86: '†', 0x87: '‡', 0x88: 'ˆ',
	0x89: '‰', 0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ',
	0x8E: 'Ž', 0x91: '‘', 0x92: '’', 0x93: '“',
	0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—',
	0x98: '˜', 0x99: '™', 0x9A: 'š', 0x9B: '›',
	0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
}

func decodeWindows1252(data []byte) []byte {
	var b strings.Builder
	b.Grow(len(data) + len(data)/8)
	var buf [4]byte
	for _, c := range data {
		var r rune
		switch {
		case c < 0x80:
			r = rune(c)
		default:
			if mapped, ok := win1252High[c]; ok {
				r = mapped
			} else {
				r = rune(c) // 0xA0–0xFF Latin-1, plus undefined slots
			}
		}
		n := utf8.EncodeRune(buf[:], r)
		b.Write(buf[:n])
	}
	return []byte(b.String())
}

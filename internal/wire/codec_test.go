package wire

import (
	"bufio"
	"bytes"
	"testing"
)

func TestUvarintRoundTrip(t *testing.T) {
	values := []uint64{
		0,
		1,
		0x7f,
		0x80,
		0xff,
		0x3fff,
		0x4000,
		0xffff,
		0x1_0000,
		0xffff_ffff,
		0x1_0000_0000,
		0xffff_ffff_ffff_ffff,
	}
	for _, v := range values {
		buf := AppendUvarint(nil, v)
		r := bufio.NewReader(bytes.NewReader(buf))
		got, err := ReadUvarint(r)
		if err != nil {
			t.Fatalf("ReadUvarint(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("round-trip mismatch: got %d want %d", got, v)
		}
		if sz := UvarintSize(v); sz != len(buf) {
			t.Fatalf("UvarintSize(%d)=%d, encoded len=%d", v, sz, len(buf))
		}
	}
}

func TestZStringRoundTrip(t *testing.T) {
	values := []string{"", "a", "hello", "a/b/c", "héllo", "key/with space"}
	for _, s := range values {
		buf := AppendZString(nil, s)
		r := bufio.NewReader(bytes.NewReader(buf))
		got, err := ReadZString(r)
		if err != nil {
			t.Fatalf("ReadZString(%q): %v", s, err)
		}
		if got != s {
			t.Fatalf("round-trip: got %q want %q", got, s)
		}
	}
}

func TestZBytesRoundTrip(t *testing.T) {
	values := [][]byte{
		nil,
		{},
		{0x00},
		{0x01, 0x02, 0x03},
		bytes.Repeat([]byte{0xAB}, 300),
	}
	for _, b := range values {
		buf := AppendZBytes(nil, b)
		r := bufio.NewReader(bytes.NewReader(buf))
		got, err := ReadZBytes(r)
		if err != nil {
			t.Fatalf("ReadZBytes len=%d: %v", len(b), err)
		}
		if len(got) != len(b) {
			t.Fatalf("round-trip length: got %d want %d", len(got), len(b))
		}
		for i := range b {
			if got[i] != b[i] {
				t.Fatalf("round-trip byte %d mismatch", i)
			}
		}
	}
}

func TestU16LERoundTrip(t *testing.T) {
	values := []uint16{0, 1, 0xff, 0x100, 0xffff}
	for _, v := range values {
		buf := AppendU16LE(nil, v)
		if len(buf) != 2 {
			t.Fatalf("AppendU16LE: got %d bytes", len(buf))
		}
		r := bufio.NewReader(bytes.NewReader(buf))
		got, err := ReadU16LE(r)
		if err != nil {
			t.Fatalf("ReadU16LE: %v", err)
		}
		if got != v {
			t.Fatalf("round-trip: got %d want %d", got, v)
		}
	}
}

func TestEncodingRoundTrip(t *testing.T) {
	cases := []struct {
		id     uint16
		schema string
	}{
		{0, ""},
		{1, ""},
		{42, "text/plain"},
		{0x7f, "application/json"},
	}
	for _, c := range cases {
		buf := AppendEncoding(nil, c.id, c.schema)
		r := bufio.NewReader(bytes.NewReader(buf))
		id, schema, err := ReadEncoding(r)
		if err != nil {
			t.Fatalf("ReadEncoding: %v", err)
		}
		if id != c.id || schema != c.schema {
			t.Fatalf("round-trip: got (%d,%q) want (%d,%q)", id, schema, c.id, c.schema)
		}
	}
}

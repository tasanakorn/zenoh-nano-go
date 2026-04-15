package wire

import (
	"encoding/binary"
	"io"
)

// AppendUvarint appends an unsigned LEB128 (Zenoh zint) varint to dst.
func AppendUvarint(dst []byte, v uint64) []byte {
	return binary.AppendUvarint(dst, v)
}

// ReadUvarint reads an unsigned LEB128 varint from r.
func ReadUvarint(r io.ByteReader) (uint64, error) {
	v, err := binary.ReadUvarint(r)
	return v, err
}

// UvarintSize returns the number of bytes needed to encode v.
func UvarintSize(v uint64) int {
	n := 1
	for v >= 0x80 {
		n++
		v >>= 7
	}
	return n
}

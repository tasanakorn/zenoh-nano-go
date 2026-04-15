package wire

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// ReadZString reads a varint-length-prefixed UTF-8 string.
func ReadZString(r *bufio.Reader) (string, error) {
	n, err := ReadUvarint(r)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// AppendZString appends a varint-length-prefixed UTF-8 string to dst.
func AppendZString(dst []byte, s string) []byte {
	dst = AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

// ReadZBytes reads a varint-length-prefixed byte slice.
func ReadZBytes(r *bufio.Reader) ([]byte, error) {
	n, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// AppendZBytes appends a varint-length-prefixed byte slice to dst.
func AppendZBytes(dst, b []byte) []byte {
	dst = AppendUvarint(dst, uint64(len(b)))
	return append(dst, b...)
}

// ReadU16LE reads a 2-byte little-endian uint16.
func ReadU16LE(r *bufio.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(buf[:]), nil
}

// AppendU16LE appends a 2-byte little-endian uint16 to dst.
func AppendU16LE(dst []byte, v uint16) []byte {
	return binary.LittleEndian.AppendUint16(dst, v)
}

// ReadCookie reads a varint-length-prefixed cookie.
func ReadCookie(r *bufio.Reader) ([]byte, error) {
	n, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// AppendCookie appends a varint-length-prefixed cookie to dst.
func AppendCookie(dst, cookie []byte) []byte {
	dst = AppendUvarint(dst, uint64(len(cookie)))
	return append(dst, cookie...)
}

// ReadEncoding reads a Zenoh Encoding: varint `id<<1 | has_schema`, optional schema string.
func ReadEncoding(r *bufio.Reader) (uint16, string, error) {
	v, err := ReadUvarint(r)
	if err != nil {
		return 0, "", err
	}
	id := uint16(v >> 1)
	hasSchema := (v & 1) == 1
	var schema string
	if hasSchema {
		schema, err = ReadZString(r)
		if err != nil {
			return 0, "", err
		}
	}
	return id, schema, nil
}

// AppendEncoding appends a Zenoh Encoding to dst.
func AppendEncoding(dst []byte, id uint16, schema string) []byte {
	v := uint64(id) << 1
	if schema != "" {
		v |= 1
	}
	dst = AppendUvarint(dst, v)
	if schema != "" {
		dst = AppendZString(dst, schema)
	}
	return dst
}

// ReadWireExpr reads a key expression from the network layer:
// Returns (declaredID, suffix, error).
// If declaredID==0: full string in suffix.
// If declaredID>0: numeric declared form; suffix present iff flags&FlagN != 0.
func ReadWireExpr(r *bufio.Reader, flags uint8) (uint16, string, error) {
	id, err := ReadUvarint(r)
	if err != nil {
		return 0, "", fmt.Errorf("wireexpr id: %w", err)
	}
	var suffix string
	if flags&FlagN != 0 {
		suffix, err = ReadZString(r)
		if err != nil {
			return 0, "", fmt.Errorf("wireexpr suffix: %w", err)
		}
	}
	return uint16(id), suffix, nil
}

// AppendWireExprString appends a string-form (undeclared) WireExpr.
// Returns the FlagN flag bit that must be set in the network message header.
func AppendWireExprString(dst []byte, keyExpr string) ([]byte, uint8) {
	dst = AppendUvarint(dst, 0) // id = 0
	dst = AppendZString(dst, keyExpr)
	return dst, FlagN
}

// AppendWireExprID appends a declared-ID WireExpr.
func AppendWireExprID(dst []byte, id uint16) []byte {
	return AppendUvarint(dst, uint64(id))
}

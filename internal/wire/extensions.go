package wire

import (
	"bufio"
	"io"
)

const (
	ExtEncodingUnit = uint8(0) // no body
	ExtEncodingZInt = uint8(1) // varint body
	ExtEncodingZBuf = uint8(2) // length-prefixed bytes
)

// Extension is a decoded Z-extension TLV.
type Extension struct {
	ID       uint8
	Encoding uint8
	Body     []byte
}

// ReadExtensions reads zero or more extensions from r.
// hasZ indicates whether the parent header had the Z flag set.
func ReadExtensions(r *bufio.Reader, hasZ bool) ([]Extension, error) {
	if !hasZ {
		return nil, nil
	}
	var exts []Extension
	for {
		hdr, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		id := hdr & 0x1f
		enc := (hdr >> 5) & 0x03
		more := (hdr & 0x80) != 0

		var body []byte
		switch enc {
		case ExtEncodingUnit:
			// no body
		case ExtEncodingZInt:
			v, err := ReadUvarint(r)
			if err != nil {
				return nil, err
			}
			body = AppendUvarint(nil, v)
		case ExtEncodingZBuf:
			body, err = ReadZBytes(r)
			if err != nil {
				return nil, err
			}
		default:
			// unknown encoding -- skip as ZBuf (best-effort)
			body, err = ReadZBytes(r)
			if err != nil {
				return nil, err
			}
		}
		exts = append(exts, Extension{ID: id, Encoding: enc, Body: body})
		if !more {
			break
		}
	}
	return exts, nil
}

// SkipExtensions skips all extensions without retaining their content.
//
// Note: zenoh extensions can be marked "mandatory" via a dedicated extension
// ID; a strict implementation would refuse to process a message carrying an
// unknown mandatory extension. For v0.1.0 we follow zenoh-pico's forward-
// compat behavior and skip every extension silently. Revisit once we decode
// enough extensions (timestamps, attachments, etc.) to need stricter checks.
func SkipExtensions(r *bufio.Reader, hasZ bool) error {
	_, err := ReadExtensions(r, hasZ)
	return err
}

// AppendExtensions encodes extensions into dst.
// Sets the "more" bit on all but the last extension.
func AppendExtensions(dst []byte, exts []Extension) []byte {
	for i, ext := range exts {
		more := i < len(exts)-1
		hdr := (ext.ID & 0x1f) | ((ext.Encoding & 0x03) << 5)
		if more {
			hdr |= 0x80
		}
		dst = append(dst, hdr)
		switch ext.Encoding {
		case ExtEncodingUnit:
			// nothing
		case ExtEncodingZInt:
			if len(ext.Body) > 0 {
				br := byteSliceReader(ext.Body)
				v, _ := ReadUvarint(&br)
				dst = AppendUvarint(dst, v)
			} else {
				dst = AppendUvarint(dst, 0)
			}
		case ExtEncodingZBuf:
			dst = AppendZBytes(dst, ext.Body)
		}
	}
	return dst
}

// byteSliceReader implements io.ByteReader over a []byte.
type byteSliceReader []byte

func (b *byteSliceReader) ReadByte() (byte, error) {
	if len(*b) == 0 {
		return 0, io.EOF
	}
	c := (*b)[0]
	*b = (*b)[1:]
	return c, nil
}

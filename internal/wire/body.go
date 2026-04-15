package wire

import (
	"bufio"
	"fmt"
	"io"
)

// PutBody is the body of a PUT network message.
type PutBody struct {
	EncodingID     uint16
	EncodingSchema string
	Payload        []byte
	Attachment     []byte
}

// DelBody is the body of a DELETE.
type DelBody struct {
	Attachment []byte
}

// QueryBody is the body of a QUERY.
type QueryBody struct {
	Parameters     string
	EncodingID     uint16
	EncodingSchema string
	Payload        []byte
	HasPayload     bool
}

// ReplyBody is the body of a REPLY.
type ReplyBody struct {
	EncodingID     uint16
	EncodingSchema string
	Payload        []byte
}

// ErrBody is the body of an ERR response.
type ErrBody struct {
	EncodingID     uint16
	EncodingSchema string
	Payload        []byte
}

// Body-level header flags.
const (
	BodyFlagT      = uint8(0x20) // Timestamp present (PUT/DEL/REPLY)
	BodyFlagI      = uint8(0x40) // Encoding present (PUT/REPLY/ERR)
	BodyFlagQParams = uint8(0x20) // parameters-present flag in QUERY body
)

// skipTimestamp reads and discards a Zenoh timestamp: u64 NTP (varint) + ZID (zbytes).
func skipTimestamp(r *bufio.Reader) error {
	if _, err := ReadUvarint(r); err != nil { // NTP time varint
		return err
	}
	if _, err := ReadZBytes(r); err != nil { // ZID as zbytes
		return err
	}
	return nil
}

// EncodePutBody encodes a PUT body (header byte + body).
func EncodePutBody(b *PutBody) []byte {
	var buf []byte
	var flags uint8
	hasEnc := b.EncodingID != 0 || b.EncodingSchema != ""
	if hasEnc {
		flags |= BodyFlagI
	}
	// Body header
	buf = append(buf, MakeHeader(ZidPut, flags))
	// Timestamp block (none). Encoding if present.
	if hasEnc {
		buf = AppendEncoding(buf, b.EncodingID, b.EncodingSchema)
	}
	// Payload (as zbytes).
	buf = AppendZBytes(buf, b.Payload)
	return buf
}

// EncodeDelBody encodes a DELETE body.
func EncodeDelBody(b *DelBody) []byte {
	var buf []byte
	buf = append(buf, MakeHeader(ZidDel, 0))
	return buf
}

// EncodeQueryBody encodes a QUERY body.
func EncodeQueryBody(b *QueryBody) []byte {
	var buf []byte
	var flags uint8
	if b.Parameters != "" {
		flags |= BodyFlagQParams
	}
	if b.HasPayload {
		flags |= BodyFlagI
	}
	buf = append(buf, MakeHeader(ZidQuery, flags))
	if b.Parameters != "" {
		buf = AppendZString(buf, b.Parameters)
	}
	// Extensions (Z flag) -- none.
	// Body (optional): if HasPayload, encode encoding + payload, else omit.
	if b.HasPayload {
		buf = AppendEncoding(buf, b.EncodingID, b.EncodingSchema)
		buf = AppendZBytes(buf, b.Payload)
	}
	return buf
}

// EncodeReplyBody encodes a REPLY body (ZidReply header + optional encoding + payload).
func EncodeReplyBody(b *ReplyBody) []byte {
	var buf []byte
	var flags uint8
	hasEnc := b.EncodingID != 0 || b.EncodingSchema != ""
	if hasEnc {
		flags |= BodyFlagI
	}
	buf = append(buf, MakeHeader(ZidReply, flags))
	if hasEnc {
		buf = AppendEncoding(buf, b.EncodingID, b.EncodingSchema)
	}
	buf = AppendZBytes(buf, b.Payload)
	return buf
}

// EncodeErrBody encodes an ERR body (ZidErr header + optional encoding + payload).
func EncodeErrBody(b *ErrBody) []byte {
	var buf []byte
	var flags uint8
	hasEnc := b.EncodingID != 0 || b.EncodingSchema != ""
	if hasEnc {
		flags |= BodyFlagI
	}
	buf = append(buf, MakeHeader(ZidErr, flags))
	if hasEnc {
		buf = AppendEncoding(buf, b.EncodingID, b.EncodingSchema)
	}
	buf = AppendZBytes(buf, b.Payload)
	return buf
}

// DecodePutBody decodes a PUT body from r given its header flags.
func DecodePutBody(r *bufio.Reader, flags uint8) (*PutBody, error) {
	hasZ := flags&FlagZ != 0
	b := &PutBody{}
	if flags&BodyFlagT != 0 {
		if err := skipTimestamp(r); err != nil {
			return nil, fmt.Errorf("put timestamp: %w", err)
		}
	}
	if flags&BodyFlagI != 0 {
		id, schema, err := ReadEncoding(r)
		if err != nil {
			return nil, err
		}
		b.EncodingID = id
		b.EncodingSchema = schema
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	// Payload is zbytes (the rest of the body).
	payload, err := ReadZBytes(r)
	if err != nil && err != io.EOF {
		return nil, err
	}
	b.Payload = payload
	return b, nil
}

// DecodeDelBody decodes a DELETE body from r.
func DecodeDelBody(r *bufio.Reader, flags uint8) (*DelBody, error) {
	hasZ := flags&FlagZ != 0
	if flags&BodyFlagT != 0 {
		if err := skipTimestamp(r); err != nil {
			return nil, fmt.Errorf("del timestamp: %w", err)
		}
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &DelBody{}, nil
}

// DecodeReplyBody decodes a REPLY body from r (similar to PUT).
func DecodeReplyBody(r *bufio.Reader, flags uint8) (*ReplyBody, error) {
	hasZ := flags&FlagZ != 0
	b := &ReplyBody{}
	if flags&BodyFlagT != 0 {
		if err := skipTimestamp(r); err != nil {
			return nil, fmt.Errorf("reply timestamp: %w", err)
		}
	}
	if flags&BodyFlagI != 0 {
		id, schema, err := ReadEncoding(r)
		if err != nil {
			return nil, err
		}
		b.EncodingID = id
		b.EncodingSchema = schema
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	payload, err := ReadZBytes(r)
	if err != nil && err != io.EOF {
		return nil, err
	}
	b.Payload = payload
	return b, nil
}

// DecodeErrBody decodes an ERR body.
func DecodeErrBody(r *bufio.Reader, flags uint8) (*ErrBody, error) {
	hasZ := flags&FlagZ != 0
	b := &ErrBody{}
	if flags&BodyFlagI != 0 {
		id, schema, err := ReadEncoding(r)
		if err != nil {
			return nil, err
		}
		b.EncodingID = id
		b.EncodingSchema = schema
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	payload, err := ReadZBytes(r)
	if err != nil && err != io.EOF {
		return nil, err
	}
	b.Payload = payload
	return b, nil
}

// DecodeQueryBody decodes a QUERY body. flags is from the body header byte
// (already consumed by DecodeBody before calling here).
func DecodeQueryBody(r *bufio.Reader, flags uint8) (*QueryBody, error) {
	hasZ := flags&FlagZ != 0
	b := &QueryBody{}
	if flags&BodyFlagQParams != 0 {
		p, err := ReadZString(r)
		if err != nil {
			return nil, err
		}
		b.Parameters = p
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	// Payload is optional; BodyFlagI signals encoding+payload present.
	if flags&BodyFlagI != 0 {
		id, schema, err := ReadEncoding(r)
		if err != nil {
			return nil, err
		}
		b.EncodingID = id
		b.EncodingSchema = schema
		payload, err := ReadZBytes(r)
		if err != nil && err != io.EOF {
			return nil, err
		}
		b.Payload = payload
		b.HasPayload = true
	}
	return b, nil
}

// DecodeBody inspects the header byte to dispatch to the right decoder.
// Returns the decoded value, the body-kind ID, and an error.
func DecodeBody(r *bufio.Reader) (interface{}, uint8, error) {
	hdr, err := r.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	mid := MsgID(hdr)
	flags := MsgFlags(hdr)
	switch mid {
	case ZidPut:
		b, err := DecodePutBody(r, flags)
		return b, mid, err
	case ZidDel:
		b, err := DecodeDelBody(r, flags)
		return b, mid, err
	case ZidReply:
		b, err := DecodeReplyBody(r, flags)
		return b, mid, err
	case ZidErr:
		b, err := DecodeErrBody(r, flags)
		return b, mid, err
	case ZidQuery:
		b, err := DecodeQueryBody(r, flags)
		return b, mid, err
	default:
		return nil, mid, fmt.Errorf("unknown body mid 0x%02x", mid)
	}
}

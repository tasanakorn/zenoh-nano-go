package wire

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// Default resolution byte: SN:U32 | ReqID:U32 in low 4 bits (2 bits each).
const DefaultResolution = uint8(0x0a)

// DefaultBatchSize is the default agreed batch size.
const DefaultBatchSize = uint16(65535)

// InitSyn is the initial handshake message sent by the client.
type InitSyn struct {
	WhatAmI    uint8 // wire index: 0=Router, 1=Peer, 2=Client
	ZID        []byte
	Resolution uint8
	BatchSize  uint16
}

// Encode returns the wire bytes of an InitSyn transport message.
// S flag is set only when proposing non-default resolution or batch size.
func (m *InitSyn) Encode() []byte {
	var buf []byte
	zidLen := len(m.ZID)
	if zidLen < 1 || zidLen > 16 {
		panic("wire: ZID length out of range")
	}
	nonDefault := m.Resolution != DefaultResolution || m.BatchSize != DefaultBatchSize
	var hdrFlags uint8
	if nonDefault {
		hdrFlags = FlagS
	}
	buf = append(buf, MakeHeader(MidInit, hdrFlags))
	buf = append(buf, ProtoVersion)
	// flags_byte = (whatami & 0x03) | ((zid_len-1) << 4)
	flagsByte := (m.WhatAmI & 0x03) | (uint8(zidLen-1) << 4)
	buf = append(buf, flagsByte)
	buf = append(buf, m.ZID...)
	if nonDefault {
		buf = append(buf, m.Resolution)
		buf = AppendU16LE(buf, m.BatchSize)
	}
	return buf
}

// InitAck is the server's response to InitSyn.
type InitAck struct {
	WhatAmI    uint8
	ZID        []byte
	Resolution uint8
	BatchSize  uint16
	Cookie     []byte
}

func decodeInitAck(r *bufio.Reader, hdrFlags uint8, hasZ bool) (*InitAck, error) {
	ver, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	_ = ver
	flagsByte, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	whatAmI := flagsByte & 0x03
	zidLen := int((flagsByte>>4)&0x0f) + 1
	zid := make([]byte, zidLen)
	if _, err := io.ReadFull(r, zid); err != nil {
		return nil, err
	}
	// Resolution and batch_size are only present when the S flag is set.
	resolution := DefaultResolution
	batchSize := DefaultBatchSize
	if hdrFlags&FlagS != 0 {
		resolution, err = r.ReadByte()
		if err != nil {
			return nil, err
		}
		batchSize, err = ReadU16LE(r)
		if err != nil {
			return nil, err
		}
	}
	cookie, err := ReadCookie(r)
	if err != nil {
		return nil, err
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &InitAck{
		WhatAmI:    whatAmI,
		ZID:        zid,
		Resolution: resolution,
		BatchSize:  batchSize,
		Cookie:     cookie,
	}, nil
}

// OpenSyn is sent after receiving InitAck.
type OpenSyn struct {
	LeaseMs   uint64
	InitialSN uint64
	Cookie    []byte
}

// Encode returns wire bytes for OpenSyn (lease in milliseconds; T flag clear).
func (m *OpenSyn) Encode() []byte {
	var buf []byte
	buf = append(buf, MakeHeader(MidOpen, 0))
	buf = AppendUvarint(buf, m.LeaseMs)
	buf = AppendUvarint(buf, m.InitialSN)
	buf = AppendCookie(buf, m.Cookie)
	return buf
}

// OpenAck is the server's response to OpenSyn.
type OpenAck struct {
	LeaseMs   uint64
	InitialSN uint64
}

func decodeOpenAck(r *bufio.Reader, hdrFlags uint8, hasZ bool) (*OpenAck, error) {
	lease, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if hdrFlags&FlagT != 0 {
		lease *= 1000
	}
	initSN, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &OpenAck{LeaseMs: lease, InitialSN: initSN}, nil
}

// Close is a transport CLOSE message.
type Close struct {
	Reason  uint8
	Session bool // if true, close whole session
}

// Encode returns wire bytes for a Close message.
func (m *Close) Encode() []byte {
	var buf []byte
	var flags uint8
	if m.Session {
		flags |= FlagS
	}
	buf = append(buf, MakeHeader(MidClose, flags))
	buf = append(buf, m.Reason)
	return buf
}

func decodeClose(r *bufio.Reader, flags uint8, hasZ bool) (*Close, error) {
	reason, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &Close{Reason: reason, Session: (flags & FlagS) != 0}, nil
}

// KeepAlive is a transport KEEP_ALIVE message.
type KeepAlive struct{}

// Encode returns wire bytes for a KeepAlive message.
func (m *KeepAlive) Encode() []byte {
	return []byte{MakeHeader(MidKeepAlive, 0)}
}

func decodeKeepAlive(r *bufio.Reader, hasZ bool) (*KeepAlive, error) {
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &KeepAlive{}, nil
}

// Frame carries zero or more network messages.
type Frame struct {
	Reliable bool
	SN       uint64
	// Payload is the concatenated encoded network messages.
	Payload []byte
}

// Encode returns wire bytes for a Frame.
func (m *Frame) Encode() []byte {
	var flags uint8
	if m.Reliable {
		flags |= FlagR
	}
	var buf []byte
	buf = append(buf, MakeHeader(MidFrame, flags))
	buf = AppendUvarint(buf, m.SN)
	buf = append(buf, m.Payload...)
	return buf
}

// DecodeFrame decodes a frame body (everything after the header byte) from data.
// data is the full remaining byte slice for this frame.
func DecodeFrame(flags uint8, data []byte) (*Frame, error) {
	hasZ := flags&FlagZ != 0
	br := bufio.NewReaderSize(bytes.NewReader(data), len(data)+1)
	sn, err := ReadUvarint(br)
	if err != nil {
		return nil, err
	}
	if hasZ {
		if err := SkipExtensions(br, true); err != nil {
			return nil, err
		}
	}
	payload, err := io.ReadAll(br)
	if err != nil {
		return nil, err
	}
	return &Frame{
		Reliable: (flags & FlagR) != 0,
		SN:       sn,
		Payload:  payload,
	}, nil
}

// DecodeFragment decodes a fragment body (everything after the header byte) from data.
func DecodeFragment(flags uint8, data []byte) (*Fragment, error) {
	hasZ := flags&FlagZ != 0
	br := bufio.NewReaderSize(bytes.NewReader(data), len(data)+1)
	sn, err := ReadUvarint(br)
	if err != nil {
		return nil, err
	}
	if hasZ {
		if err := SkipExtensions(br, true); err != nil {
			return nil, err
		}
	}
	payload, err := io.ReadAll(br)
	if err != nil {
		return nil, err
	}
	return &Fragment{
		Reliable: (flags & FlagR) != 0,
		More:     (flags & FlagM) != 0,
		SN:       sn,
		Payload:  payload,
	}, nil
}

// Fragment is a fragmented portion of a large network message.
type Fragment struct {
	Reliable bool
	More     bool
	SN       uint64
	Payload  []byte
}

// Encode returns wire bytes for a Fragment.
func (m *Fragment) Encode() []byte {
	var flags uint8
	if m.Reliable {
		flags |= FlagR
	}
	if m.More {
		flags |= FlagM
	}
	var buf []byte
	buf = append(buf, MakeHeader(MidFragment, flags))
	buf = AppendUvarint(buf, m.SN)
	buf = append(buf, m.Payload...)
	return buf
}

// DecodeTransport parses a single transport message from an exact-length byte slice.
// Returns one of: *InitAck, *OpenAck, *Close, *KeepAlive, *Frame, *Fragment, *InitSyn, *OpenSyn.
func DecodeTransport(data []byte) (interface{}, error) {
	if len(data) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	hdr := data[0]
	mid := MsgID(hdr)
	flags := MsgFlags(hdr)
	hasZ := flags&FlagZ != 0
	rest := data[1:]
	r := bufio.NewReader(bytes.NewReader(rest))
	switch mid {
	case MidInit:
		if flags&FlagA != 0 {
			return decodeInitAck(r, flags, hasZ)
		}
		return nil, fmt.Errorf("unexpected InitSyn from peer")
	case MidOpen:
		if flags&FlagA != 0 {
			return decodeOpenAck(r, flags, hasZ)
		}
		return nil, fmt.Errorf("unexpected OpenSyn from peer")
	case MidClose:
		return decodeClose(r, flags, hasZ)
	case MidKeepAlive:
		return decodeKeepAlive(r, hasZ)
	case MidFrame:
		return DecodeFrame(flags, rest)
	case MidFragment:
		return DecodeFragment(flags, rest)
	default:
		return nil, fmt.Errorf("unknown transport mid 0x%02x", mid)
	}
}

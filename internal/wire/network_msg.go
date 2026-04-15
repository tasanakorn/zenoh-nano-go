package wire

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// PushMsg is a network PUSH message carrying a PUT or DEL body.
type PushMsg struct {
	DeclaredID uint16
	KeyExpr    string // empty if using DeclaredID
	Body       []byte // encoded body (already includes body header)
}

// Encode encodes a Push network message.
func (m *PushMsg) Encode() []byte {
	var buf []byte
	var flags uint8
	if m.KeyExpr != "" {
		flags |= FlagN
	}
	buf = append(buf, MakeHeader(NidPush, flags))
	// WireExpr
	buf = AppendUvarint(buf, uint64(m.DeclaredID))
	if m.KeyExpr != "" {
		buf = AppendZString(buf, m.KeyExpr)
	}
	// Body
	buf = append(buf, m.Body...)
	return buf
}

// DecodedPush is the result of decoding a PUSH network message.
type DecodedPush struct {
	DeclaredID uint16
	KeyExpr    string
	BodyMID    uint8
	Put        *PutBody
	Del        *DelBody
}

// DecodePush decodes a PUSH network message from an exact-length slice.
func DecodePush(data []byte, flags uint8) (*DecodedPush, error) {
	return decodePushFromReader(bufio.NewReader(bytes.NewReader(data)), flags)
}

func decodePushFromReader(r *bufio.Reader, flags uint8) (*DecodedPush, error) {
	hasZ := flags&FlagZ != 0
	declID, suffix, err := ReadWireExpr(r, flags)
	if err != nil {
		return nil, err
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	p := &DecodedPush{DeclaredID: declID, KeyExpr: suffix}
	body, mid, err := DecodeBody(r)
	if err != nil {
		return nil, err
	}
	p.BodyMID = mid
	switch bb := body.(type) {
	case *PutBody:
		p.Put = bb
	case *DelBody:
		p.Del = bb
	}
	return p, nil
}

// RequestMsg is a network REQUEST message (used for Get).
type RequestMsg struct {
	RequestID  uint64
	DeclaredID uint16
	KeyExpr    string
	Body       []byte // encoded body (includes body header)
}

// Encode encodes a REQUEST network message.
func (m *RequestMsg) Encode() []byte {
	var buf []byte
	var flags uint8
	if m.KeyExpr != "" {
		flags |= FlagN
	}
	buf = append(buf, MakeHeader(NidRequest, flags))
	buf = AppendUvarint(buf, m.RequestID)
	buf = AppendUvarint(buf, uint64(m.DeclaredID))
	if m.KeyExpr != "" {
		buf = AppendZString(buf, m.KeyExpr)
	}
	buf = append(buf, m.Body...)
	return buf
}

// DecodedResponse is the decoded form of a RESPONSE network message.
type DecodedResponse struct {
	RequestID  uint64
	DeclaredID uint16
	KeyExpr    string
	BodyMID    uint8
	Reply      *ReplyBody
	Put        *PutBody
	Del        *DelBody
	Err        *ErrBody
}

// DecodeResponse decodes a RESPONSE network message from an exact-length slice.
func DecodeResponse(data []byte, flags uint8) (*DecodedResponse, error) {
	return decodeResponseFromReader(bufio.NewReader(bytes.NewReader(data)), flags)
}

func decodeResponseFromReader(r *bufio.Reader, flags uint8) (*DecodedResponse, error) {
	hasZ := flags&FlagZ != 0
	reqID, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	declID, suffix, err := ReadWireExpr(r, flags)
	if err != nil {
		return nil, err
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	resp := &DecodedResponse{RequestID: reqID, DeclaredID: declID, KeyExpr: suffix}
	body, mid, err := DecodeBody(r)
	if err != nil && err != io.EOF {
		return nil, err
	}
	resp.BodyMID = mid
	switch bb := body.(type) {
	case *ReplyBody:
		resp.Reply = bb
	case *PutBody:
		resp.Put = bb
	case *DelBody:
		resp.Del = bb
	case *ErrBody:
		resp.Err = bb
	}
	return resp, nil
}

// DecodedResponseFinal is the decoded form of a RESPONSE_FINAL network message.
type DecodedResponseFinal struct {
	RequestID uint64
}

// DecodeResponseFinal decodes a RESPONSE_FINAL message from an exact-length slice.
func DecodeResponseFinal(data []byte, flags uint8) (*DecodedResponseFinal, error) {
	return decodeResponseFinalFromReader(bufio.NewReader(bytes.NewReader(data)), flags)
}

func decodeResponseFinalFromReader(r *bufio.Reader, flags uint8) (*DecodedResponseFinal, error) {
	hasZ := flags&FlagZ != 0
	reqID, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &DecodedResponseFinal{RequestID: reqID}, nil
}

// DeclareMsg is a network DECLARE message.
type DeclareMsg struct {
	Bodies []DeclareBody
}

// Encode encodes a DECLARE network message.
// TODO(v0.2): consider appending a DeclFinal body (id=0x1a) after client
// subscribe/unsubscribe declarations. zenohd sends DeclFinal to clients, but
// clients are not required to emit it for the declaration kinds we currently
// use. Revisit once we support queryables and key-expression interning.
func (m *DeclareMsg) Encode() []byte {
	var buf []byte
	buf = append(buf, MakeHeader(NidDeclare, 0))
	for _, b := range m.Bodies {
		buf = append(buf, EncodeDeclareBody(b)...)
	}
	return buf
}

// DecodeDeclare decodes a DECLARE network message from an exact-length slice.
func DecodeDeclare(data []byte, flags uint8) (*DeclareMsg, error) {
	hasZ := flags&FlagZ != 0
	r := bufio.NewReader(bytes.NewReader(data))
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	m := &DeclareMsg{}
	for {
		b, err := DecodeDeclareBody(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			// Unknown or malformed; stop.
			break
		}
		m.Bodies = append(m.Bodies, b)
		if _, err := r.Peek(1); err == io.EOF {
			break
		}
	}
	return m, nil
}

// decodeDeclareFromReader decodes a DECLARE network message from a reader.
// Unlike DecodeDeclare, it stops after a DeclFinal body or when a byte cannot
// be peeked (EOF). It does not consume bytes belonging to subsequent network
// messages in a batched frame payload. Since declarations lack explicit
// boundaries, the only robust terminator in a batched stream is a DeclFinal.
func decodeDeclareFromReader(r *bufio.Reader, flags uint8) (*DeclareMsg, error) {
	hasZ := flags&FlagZ != 0
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	m := &DeclareMsg{}
	for {
		b, err := DecodeDeclareBody(r)
		if err == io.EOF {
			return m, nil
		}
		if err != nil {
			return m, nil
		}
		m.Bodies = append(m.Bodies, b)
		if _, ok := b.(*DeclFinalBody); ok {
			return m, nil
		}
		if _, err := r.Peek(1); err == io.EOF {
			return m, nil
		}
	}
}

// DecodedNetwork is the decoded form of one network message.
type DecodedNetwork struct {
	Kind          uint8 // NidPush, NidDeclare, NidRequest, NidResponse, NidResponseFinal
	Push          *DecodedPush
	Declare       *DeclareMsg
	Response      *DecodedResponse
	ResponseFinal *DecodedResponseFinal
}

// DecodeNetworkStream decodes a sequence of network messages from a frame payload.
// zenohd batches multiple network messages per frame; parse until EOF.
func DecodeNetworkStream(payload []byte) ([]DecodedNetwork, error) {
	var out []DecodedNetwork
	r := bufio.NewReader(bytes.NewReader(payload))
	for {
		hdr, err := r.ReadByte()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		mid := MsgID(hdr)
		flags := MsgFlags(hdr)
		switch mid {
		case NidPush:
			p, err := decodePushFromReader(r, flags)
			if err != nil {
				return out, err
			}
			out = append(out, DecodedNetwork{Kind: NidPush, Push: p})
		case NidDeclare:
			d, err := decodeDeclareFromReader(r, flags)
			if err != nil {
				return out, err
			}
			out = append(out, DecodedNetwork{Kind: NidDeclare, Declare: d})
		case NidResponse:
			rsp, err := decodeResponseFromReader(r, flags)
			if err != nil {
				return out, err
			}
			out = append(out, DecodedNetwork{Kind: NidResponse, Response: rsp})
		case NidResponseFinal:
			rf, err := decodeResponseFinalFromReader(r, flags)
			if err != nil {
				return out, err
			}
			out = append(out, DecodedNetwork{Kind: NidResponseFinal, ResponseFinal: rf})
		case NidInterest, NidOAM, NidRequest:
			// Ignored / not expected on client. Without explicit length, we cannot
			// skip past a message of this kind safely within a batched frame, so
			// treat any remaining bytes as consumed.
			_, _ = io.ReadAll(r)
			return out, nil
		default:
			return out, fmt.Errorf("unknown network mid 0x%02x", mid)
		}
	}
}

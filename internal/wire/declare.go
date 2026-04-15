package wire

import (
	"bufio"
	"fmt"
	"io"
)

// DeclareBody is a single declaration within a Declare network message.
type DeclareBody interface {
	declareBodyID() uint8
}

// DeclKeyExprBody binds a numeric ID to a key expression string.
type DeclKeyExprBody struct {
	ExprID uint16
	Expr   string
}

func (*DeclKeyExprBody) declareBodyID() uint8 { return DeclKeyExpr }

// UndeclKeyExprBody unbinds a key expression ID.
type UndeclKeyExprBody struct {
	ExprID uint16
}

func (*UndeclKeyExprBody) declareBodyID() uint8 { return UndeclKeyExpr }

// DeclSubscriberBody declares a subscriber.
type DeclSubscriberBody struct {
	SubID      uint32
	DeclaredID uint16
	KeyExpr    string // empty if DeclaredID != 0
}

func (*DeclSubscriberBody) declareBodyID() uint8 { return DeclSub }

// UndeclSubscriberBody undeclares a subscriber.
type UndeclSubscriberBody struct {
	SubID uint32
	// Extensions may include the key expression; omitted here.
}

func (*UndeclSubscriberBody) declareBodyID() uint8 { return UndeclSub }

// DeclQueryableBody declares a queryable key expression.
type DeclQueryableBody struct {
	QueryableID uint32
	DeclaredID  uint16
	KeyExpr     string
}

func (*DeclQueryableBody) declareBodyID() uint8 { return DeclQueryable }

// UndeclQueryableBody undeclares a queryable.
type UndeclQueryableBody struct {
	QueryableID uint32
}

func (*UndeclQueryableBody) declareBodyID() uint8 { return UndeclQue }

// DeclFinalBody is sent to mark the end of a declaration sequence (handled silently).
type DeclFinalBody struct{}

func (*DeclFinalBody) declareBodyID() uint8 { return DeclFinal }

// EncodeDeclareBody encodes a single declaration body (header + contents).
func EncodeDeclareBody(b DeclareBody) []byte {
	switch d := b.(type) {
	case *DeclKeyExprBody:
		var buf []byte
		buf = append(buf, MakeHeader(DeclKeyExpr, 0))
		buf = AppendUvarint(buf, uint64(d.ExprID))
		buf = AppendZString(buf, d.Expr)
		return buf
	case *UndeclKeyExprBody:
		var buf []byte
		buf = append(buf, MakeHeader(UndeclKeyExpr, 0))
		buf = AppendUvarint(buf, uint64(d.ExprID))
		return buf
	case *DeclSubscriberBody:
		var buf []byte
		var flags uint8
		if d.KeyExpr != "" {
			flags |= FlagN
		}
		buf = append(buf, MakeHeader(DeclSub, flags))
		buf = AppendUvarint(buf, uint64(d.SubID))
		// WireExpr
		buf = AppendUvarint(buf, uint64(d.DeclaredID))
		if d.KeyExpr != "" {
			buf = AppendZString(buf, d.KeyExpr)
		}
		return buf
	case *UndeclSubscriberBody:
		var buf []byte
		buf = append(buf, MakeHeader(UndeclSub, 0))
		buf = AppendUvarint(buf, uint64(d.SubID))
		// WireExpr: id=0, no suffix (FlagN not set)
		buf = AppendUvarint(buf, 0)
		return buf
	case *DeclQueryableBody:
		var buf []byte
		var flags uint8
		if d.KeyExpr != "" {
			flags |= FlagN
		}
		buf = append(buf, MakeHeader(DeclQueryable, flags))
		buf = AppendUvarint(buf, uint64(d.QueryableID))
		buf = AppendUvarint(buf, uint64(d.DeclaredID))
		if d.KeyExpr != "" {
			buf = AppendZString(buf, d.KeyExpr)
		}
		return buf
	case *UndeclQueryableBody:
		var buf []byte
		buf = append(buf, MakeHeader(UndeclQue, 0))
		buf = AppendUvarint(buf, uint64(d.QueryableID))
		buf = AppendUvarint(buf, 0)
		return buf
	case *DeclFinalBody:
		return []byte{MakeHeader(DeclFinal, 0)}
	default:
		panic(fmt.Sprintf("unknown declare body type %T", b))
	}
}

// DecodeDeclareBody reads one declaration body from r. Returns the body and any error.
func DecodeDeclareBody(r *bufio.Reader) (DeclareBody, error) {
	hdr, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	mid := MsgID(hdr)
	flags := MsgFlags(hdr)
	hasZ := flags&FlagZ != 0
	switch mid {
	case DeclKeyExpr:
		id, err := ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		expr, err := ReadZString(r)
		if err != nil {
			return nil, err
		}
		if err := SkipExtensions(r, hasZ); err != nil {
			return nil, err
		}
		return &DeclKeyExprBody{ExprID: uint16(id), Expr: expr}, nil
	case UndeclKeyExpr:
		id, err := ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		if err := SkipExtensions(r, hasZ); err != nil {
			return nil, err
		}
		return &UndeclKeyExprBody{ExprID: uint16(id)}, nil
	case DeclSub:
		subID, err := ReadUvarint(r)
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
		return &DeclSubscriberBody{
			SubID:      uint32(subID),
			DeclaredID: declID,
			KeyExpr:    suffix,
		}, nil
	case UndeclSub:
		subID, err := ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		// WireExpr follows (may have FlagN).
		_, _, _ = ReadWireExpr(r, flags)
		if err := SkipExtensions(r, hasZ); err != nil {
			return nil, err
		}
		return &UndeclSubscriberBody{SubID: uint32(subID)}, nil
	case DeclQueryable:
		qid, err := ReadUvarint(r)
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
		return &DeclQueryableBody{
			QueryableID: uint32(qid),
			DeclaredID:  declID,
			KeyExpr:     suffix,
		}, nil
	case UndeclQue:
		qid, err := ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		_, _, _ = ReadWireExpr(r, flags)
		if err := SkipExtensions(r, hasZ); err != nil {
			return nil, err
		}
		return &UndeclQueryableBody{QueryableID: uint32(qid)}, nil
	case DeclFinal:
		if err := SkipExtensions(r, hasZ); err != nil {
			return nil, err
		}
		return &DeclFinalBody{}, nil
	default:
		// Unknown declaration body: consume the rest silently by reading until EOF.
		_, _ = io.ReadAll(r)
		return nil, fmt.Errorf("unknown declare body mid 0x%02x", mid)
	}
}

package wire

import (
	"bytes"
	"testing"
)

func TestInitSynEncodeDecode(t *testing.T) {
	t.Run("default values — no S flag", func(t *testing.T) {
		// When proposing exactly the defaults, S flag must be absent.
		msg := &InitSyn{
			WhatAmI:    WhatAmIClientIdx,
			ZID:        []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x01, 0x02},
			Resolution: DefaultResolution,
			BatchSize:  DefaultBatchSize,
		}
		buf := msg.Encode()
		if MsgFlags(buf[0])&FlagS != 0 {
			t.Fatal("S flag must not be set for default values")
		}
	})

	t.Run("non-default batch size — S flag set", func(t *testing.T) {
		// When proposing non-defaults, S flag must be present and params included.
		msg := &InitSyn{
			WhatAmI:    WhatAmIClientIdx,
			ZID:        []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x01, 0x02},
			Resolution: DefaultResolution,
			BatchSize:  1024,
		}
		buf := msg.Encode()
		if len(buf) == 0 {
			t.Fatal("empty encode")
		}
		if MsgID(buf[0]) != MidInit {
			t.Fatalf("header mid = 0x%02x, want 0x%02x", MsgID(buf[0]), MidInit)
		}
		if MsgFlags(buf[0])&FlagS == 0 {
			t.Fatal("S flag must be set for non-default batch size")
		}

		// Layout: [hdr][ver][flags][zid...][resolution][batchSize:u16LE]
		r := bytes.NewReader(buf[1:])
		ver, _ := r.ReadByte()
		if ver != ProtoVersion {
			t.Fatalf("version = 0x%02x, want 0x%02x", ver, ProtoVersion)
		}
		flags, _ := r.ReadByte()
		whatAmI := flags & 0x03
		zidLen := int((flags>>4)&0x0f) + 1
		if whatAmI != msg.WhatAmI {
			t.Fatalf("whatAmI = %d, want %d", whatAmI, msg.WhatAmI)
		}
		if zidLen != len(msg.ZID) {
			t.Fatalf("zidLen = %d, want %d", zidLen, len(msg.ZID))
		}
		zid := make([]byte, zidLen)
		if _, err := r.Read(zid); err != nil {
			t.Fatalf("read zid: %v", err)
		}
		if !bytes.Equal(zid, msg.ZID) {
			t.Fatalf("zid mismatch: got %x want %x", zid, msg.ZID)
		}
		res, _ := r.ReadByte()
		if res != msg.Resolution {
			t.Fatalf("resolution = 0x%02x, want 0x%02x", res, msg.Resolution)
		}
		var lo, hi byte
		lo, _ = r.ReadByte()
		hi, _ = r.ReadByte()
		batch := uint16(lo) | uint16(hi)<<8
		if batch != msg.BatchSize {
				t.Fatalf("batchSize = %d, want %d", batch, msg.BatchSize)
			}
			if r.Len() != 0 {
				t.Fatalf("unexpected trailing bytes: %d", r.Len())
			}
		})
}

func TestInitAckEncodeDecodeRoundTrip(t *testing.T) {
	// Build an InitAck on the wire in the same layout the server would send,
	// then round-trip via DecodeTransport.
	ack := &InitAck{
		WhatAmI:    WhatAmIRouterIdx,
		ZID:        []byte{0x11, 0x22, 0x33, 0x44},
		Resolution: 0x0a,
		BatchSize:  4096,
		Cookie:     []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}
	var buf []byte
	buf = append(buf, MakeHeader(MidInit, FlagA|FlagS))
	buf = append(buf, ProtoVersion)
	zidLen := len(ack.ZID)
	buf = append(buf, (ack.WhatAmI&0x03)|(uint8(zidLen-1)<<4))
	buf = append(buf, ack.ZID...)
	buf = append(buf, ack.Resolution)
	buf = AppendU16LE(buf, ack.BatchSize)
	buf = AppendCookie(buf, ack.Cookie)

	msg, err := DecodeTransport(buf)
	if err != nil {
		t.Fatalf("DecodeTransport: %v", err)
	}
	got, ok := msg.(*InitAck)
	if !ok {
		t.Fatalf("decoded type %T, want *InitAck", msg)
	}
	if got.WhatAmI != ack.WhatAmI {
		t.Fatalf("WhatAmI = %d want %d", got.WhatAmI, ack.WhatAmI)
	}
	if !bytes.Equal(got.ZID, ack.ZID) {
		t.Fatalf("ZID mismatch")
	}
	if got.Resolution != ack.Resolution {
		t.Fatalf("Resolution mismatch")
	}
	if got.BatchSize != ack.BatchSize {
		t.Fatalf("BatchSize mismatch")
	}
	if !bytes.Equal(got.Cookie, ack.Cookie) {
		t.Fatalf("Cookie mismatch")
	}
}

func TestFrameRoundTripNoExtensions(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	f := &Frame{Reliable: true, SN: 42, Payload: payload}
	encoded := f.Encode()
	msg, err := DecodeTransport(encoded)
	if err != nil {
		t.Fatalf("DecodeTransport: %v", err)
	}
	got, ok := msg.(*Frame)
	if !ok {
		t.Fatalf("decoded type %T, want *Frame", msg)
	}
	if got.SN != f.SN || got.Reliable != f.Reliable {
		t.Fatalf("SN/Reliable mismatch: got (%d,%v) want (%d,%v)", got.SN, got.Reliable, f.SN, f.Reliable)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload mismatch: got %x want %x", got.Payload, payload)
	}
}

func TestFragmentRoundTrip(t *testing.T) {
	payload := []byte{0xAA, 0xBB, 0xCC}
	f := &Fragment{Reliable: true, More: true, SN: 7, Payload: payload}
	encoded := f.Encode()
	msg, err := DecodeTransport(encoded)
	if err != nil {
		t.Fatalf("DecodeTransport: %v", err)
	}
	got, ok := msg.(*Fragment)
	if !ok {
		t.Fatalf("decoded type %T, want *Fragment", msg)
	}
	if got.SN != f.SN || got.Reliable != f.Reliable || got.More != f.More {
		t.Fatalf("flags/SN mismatch")
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestKeepAliveEncodeDecode(t *testing.T) {
	ka := &KeepAlive{}
	encoded := ka.Encode()
	if len(encoded) != 1 {
		t.Fatalf("encoded length = %d, want 1", len(encoded))
	}
	msg, err := DecodeTransport(encoded)
	if err != nil {
		t.Fatalf("DecodeTransport: %v", err)
	}
	if _, ok := msg.(*KeepAlive); !ok {
		t.Fatalf("decoded type %T, want *KeepAlive", msg)
	}
}

func TestCloseEncodeDecode(t *testing.T) {
	c := &Close{Reason: 3, Session: true}
	encoded := c.Encode()
	msg, err := DecodeTransport(encoded)
	if err != nil {
		t.Fatalf("DecodeTransport: %v", err)
	}
	got, ok := msg.(*Close)
	if !ok {
		t.Fatalf("decoded type %T, want *Close", msg)
	}
	if got.Reason != c.Reason || got.Session != c.Session {
		t.Fatalf("mismatch: got %+v want %+v", got, c)
	}
}

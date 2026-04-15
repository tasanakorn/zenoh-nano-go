package wire

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// ScoutMsg is a scouting message sent over UDP multicast/unicast.
type ScoutMsg struct {
	Version uint8 // protocol version, usually ProtoVersion
	What    uint8 // bitmask: Router=1, Peer=2, Client=4
	ZID     []byte
}

// Encode encodes a Scout message.
func (m *ScoutMsg) Encode() []byte {
	var buf []byte
	var flags uint8
	if len(m.ZID) > 0 {
		flags |= FlagI
	}
	buf = append(buf, MakeHeader(MidScout, flags))
	buf = append(buf, m.Version)
	// what byte: low 3 bits = whatami bitmask, high 4 bits = zid_len-1 when ZID present
	w := m.What & 0x07
	if len(m.ZID) > 0 {
		w |= uint8(len(m.ZID)-1) << 4
	}
	buf = append(buf, w)
	if len(m.ZID) > 0 {
		buf = append(buf, m.ZID...)
	}
	return buf
}

// HelloMsg is a scouting Hello reply.
type HelloMsg struct {
	Version  uint8
	WhatAmI  uint8 // index: 0=Router, 1=Peer, 2=Client
	ZID      []byte
	Locators []string
}

// DecodeHello decodes a Hello message from raw bytes (starts at header).
func DecodeHello(data []byte) (*HelloMsg, error) {
	if len(data) < 1 {
		return nil, io.ErrUnexpectedEOF
	}
	hdr := data[0]
	mid := MsgID(hdr)
	if mid != MidHello {
		return nil, fmt.Errorf("not a hello message: mid=0x%02x", mid)
	}
	flags := MsgFlags(hdr)
	hasL := flags&FlagL != 0
	hasZ := flags&FlagZ != 0
	r := bufio.NewReader(bytes.NewReader(data[1:]))
	ver, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	// what byte: low 2 bits = whatami index, high 4 bits = zid_len-1
	whatByte, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	whatAmI := whatByte & 0x03
	zidLen := int((whatByte>>4)&0x0f) + 1
	zid := make([]byte, zidLen)
	if _, err := io.ReadFull(r, zid); err != nil {
		return nil, err
	}
	var locators []string
	if hasL {
		n, err := ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		for i := uint64(0); i < n; i++ {
			s, err := ReadZString(r)
			if err != nil {
				return nil, err
			}
			locators = append(locators, s)
		}
	}
	if err := SkipExtensions(r, hasZ); err != nil {
		return nil, err
	}
	return &HelloMsg{
		Version:  ver,
		WhatAmI:  whatAmI,
		ZID:      zid,
		Locators: locators,
	}, nil
}

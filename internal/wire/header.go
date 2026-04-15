package wire

// Transport message IDs (low 5 bits of header byte).
const (
	MidInit      = uint8(0x01)
	MidOpen      = uint8(0x02)
	MidClose     = uint8(0x03)
	MidKeepAlive = uint8(0x04)
	MidFrame     = uint8(0x05)
	MidFragment  = uint8(0x06)
	MidJoin      = uint8(0x07)
)

// Scouting message IDs.
const (
	MidScout = uint8(0x01)
	MidHello = uint8(0x02)
)

// Network message IDs.
const (
	NidOAM           = uint8(0x1f)
	NidDeclare       = uint8(0x1e)
	NidPush          = uint8(0x1d)
	NidRequest       = uint8(0x1c)
	NidResponse      = uint8(0x1b)
	NidResponseFinal = uint8(0x1a)
	NidInterest      = uint8(0x19)
)

// Zenoh body message IDs.
const (
	ZidPut   = uint8(0x01)
	ZidDel   = uint8(0x02)
	ZidQuery = uint8(0x03)
	ZidReply = uint8(0x04)
	ZidErr   = uint8(0x05)
)

// Declaration body IDs.
const (
	DeclKeyExpr   = uint8(0x00)
	UndeclKeyExpr = uint8(0x01)
	DeclSub       = uint8(0x02)
	UndeclSub     = uint8(0x03)
	DeclQueryable = uint8(0x04)
	UndeclQue     = uint8(0x05)
	DeclFinal     = uint8(0x1a)
)

// Header byte flag masks.
const (
	FlagA = uint8(0x20) // Ack (INIT, OPEN)
	FlagS = uint8(0x40) // Size params present (INIT)
	FlagZ = uint8(0x80) // Extensions follow
	FlagT = uint8(0x40) // Lease in seconds (OPEN)
	FlagR = uint8(0x20) // Reliable (FRAME, FRAGMENT)
	FlagM = uint8(0x40) // More fragments (FRAGMENT)
	FlagL = uint8(0x20) // Locators present (HELLO)
	FlagN = uint8(0x20) // Named (string) keyexpr in network msgs
	FlagI = uint8(0x08) // ZID present (SCOUT)
)

// MsgID extracts the 5-bit message ID from a header byte.
func MsgID(h byte) uint8 { return h & 0x1f }

// MsgFlags extracts the 3-bit flags from a header byte.
func MsgFlags(h byte) uint8 { return h & 0xe0 }

// MakeHeader builds a header byte from ID + flags.
func MakeHeader(mid, flags uint8) byte { return (flags & 0xe0) | (mid & 0x1f) }

// WhatAmI wire encoding constants (2-bit index, used in INIT/HELLO).
const (
	WhatAmIRouterIdx = uint8(0)
	WhatAmIPeerIdx   = uint8(1)
	WhatAmIClientIdx = uint8(2)
)

// WhatAmI bitmask constants (used in Scout `what` field).
const (
	WhatAmIRouterBit = uint8(0x01)
	WhatAmIPeerBit   = uint8(0x02)
	WhatAmIClientBit = uint8(0x04)
)

// ProtoVersion is the Zenoh wire protocol version.
const ProtoVersion = uint8(0x09)

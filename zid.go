package zenoh

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// ZenohID is a Zenoh peer identifier, up to 16 bytes.
type ZenohID struct {
	b   [16]byte
	len int // number of significant bytes (1-16)
}

// NewRandomZID generates a random 16-byte ZenohID.
func NewRandomZID() ZenohID {
	var z ZenohID
	if _, err := rand.Read(z.b[:]); err != nil {
		panic("zenoh: cannot generate random ZID: " + err.Error())
	}
	z.len = 16
	return z
}

// Bytes returns the significant bytes of the ZID.
func (z ZenohID) Bytes() []byte {
	return z.b[:z.len]
}

// Len returns the number of significant bytes.
func (z ZenohID) Len() int { return z.len }

// IsZero reports whether the ZID has no significant bytes.
func (z ZenohID) IsZero() bool { return z.len == 0 }

// String returns a lowercase hex representation.
func (z ZenohID) String() string {
	return hex.EncodeToString(z.b[:z.len])
}

// ZIDFromBytes creates a ZenohID from raw bytes (1-16).
func ZIDFromBytes(b []byte) (ZenohID, error) {
	if len(b) < 1 || len(b) > 16 {
		return ZenohID{}, fmt.Errorf("zenoh: ZID length %d out of range [1,16]", len(b))
	}
	var z ZenohID
	copy(z.b[:], b)
	z.len = len(b)
	return z, nil
}

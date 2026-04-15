package zenoh

// Encoding describes the format of a payload.
type Encoding struct {
	ID     uint16
	Schema string
}

// Standard Zenoh encoding IDs (wire protocol 0x09).
const (
	EncIDZenohBytes   uint16 = 0
	EncIDZenohInt64   uint16 = 1
	EncIDZenohFloat64 uint16 = 2
	EncIDZenohString  uint16 = 9
	EncIDTextPlain    uint16 = 11
	EncIDAppJSON      uint16 = 15
	EncIDAppOctet     uint16 = 16
)

var (
	EncodingDefault = Encoding{ID: EncIDZenohBytes}
	EncodingText    = Encoding{ID: EncIDTextPlain}
	EncodingJSON    = Encoding{ID: EncIDAppJSON}
)

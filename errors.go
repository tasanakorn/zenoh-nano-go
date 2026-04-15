package zenoh

import "fmt"

type ErrorCategory int

const (
	ErrCatConnection ErrorCategory = iota
	ErrCatProtocol
	ErrCatTimeout
	ErrCatClosed
	ErrCatInvalidArg
)

type ZError struct {
	Category ErrorCategory
	Op       string
	Err      error
}

func (e *ZError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("zenoh %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("zenoh %s", e.Op)
}
func (e *ZError) Unwrap() error { return e.Err }

var (
	ErrSessionClosed  = &ZError{Category: ErrCatClosed, Op: "session closed"}
	ErrTimeout        = &ZError{Category: ErrCatTimeout, Op: "timeout"}
	ErrInvalidKeyExpr = &ZError{Category: ErrCatInvalidArg, Op: "invalid key expression"}
	ErrScoutTimeout   = &ZError{Category: ErrCatTimeout, Op: "scout timeout: no hello received"}
	ErrBackpressure   = &ZError{Category: ErrCatConnection, Op: "backpressure: write channel full"}
)

func wrapErr(cat ErrorCategory, op string, err error) error {
	return &ZError{Category: cat, Op: op, Err: err}
}

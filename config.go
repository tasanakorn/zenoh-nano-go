package zenoh

import "time"

// Config configures how Open dials and runs a session.
type Config struct {
	// Connect is the ordered list of locators to try, e.g. "tcp/127.0.0.1:7447".
	Connect []string
	// Lease is the keep-alive lease advertised to the peer. Default: 10s.
	Lease time.Duration
	// HandshakeTimeout bounds the time to complete INIT/OPEN. Default: 5s.
	HandshakeTimeout time.Duration
	// ZID of this client. Zero => random.
	ZID ZenohID
	// WriteQueueSize caps the in-memory buffer of pending outbound messages. Default: 256.
	WriteQueueSize int
}

// DefaultConfig returns a Config configured for a local zenohd.
func DefaultConfig() *Config {
	return &Config{
		Connect:          []string{"tcp/127.0.0.1:7447"},
		Lease:            10 * time.Second,
		HandshakeTimeout: 5 * time.Second,
		WriteQueueSize:   256,
	}
}

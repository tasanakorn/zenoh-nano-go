// Package session implements the transport and demux layer for Zenoh client sessions.
package session

import (
	"context"
	"fmt"
	"strings"
)

// Transport is a bidirectional, message-framed transport to a Zenoh peer.
type Transport interface {
	// WriteFrame writes a single transport-layer message (already encoded).
	WriteFrame(data []byte) error
	// ReadFrame returns the next transport-layer message bytes (header + payload).
	ReadFrame() ([]byte, error)
	// Close closes the underlying connection.
	Close() error
	// RemoteLocator returns a human-readable remote address string.
	RemoteLocator() string
}

// ParseLocator parses "scheme/host:port" into scheme and address.
func ParseLocator(loc string) (scheme, addr string, err error) {
	idx := strings.Index(loc, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid locator %q: missing scheme", loc)
	}
	return loc[:idx], loc[idx+1:], nil
}

// DialTransport dials the given locator and returns a Transport.
func DialTransport(ctx context.Context, locator string) (Transport, error) {
	scheme, addr, err := ParseLocator(locator)
	if err != nil {
		return nil, err
	}
	switch scheme {
	case "tcp":
		return dialTCP(ctx, addr)
	case "udp":
		return dialUDP(ctx, addr)
	default:
		return nil, fmt.Errorf("unsupported scheme %q", scheme)
	}
}

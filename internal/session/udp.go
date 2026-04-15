package session

import (
	"context"
	"net"
)

type udpTransport struct {
	conn   *net.UDPConn
	remote string
	buf    []byte
}

func dialUDP(ctx context.Context, addr string) (Transport, error) {
	uaddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", uaddr.String())
	if err != nil {
		return nil, err
	}
	return &udpTransport{
		conn:   c.(*net.UDPConn),
		remote: "udp/" + addr,
		buf:    make([]byte, 65536),
	}, nil
}

// ReadFrame reads a single UDP datagram.
func (t *udpTransport) ReadFrame() ([]byte, error) {
	n, err := t.conn.Read(t.buf)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, t.buf[:n])
	return out, nil
}

// WriteFrame writes a UDP datagram.
func (t *udpTransport) WriteFrame(data []byte) error {
	_, err := t.conn.Write(data)
	return err
}

func (t *udpTransport) Close() error          { return t.conn.Close() }
func (t *udpTransport) RemoteLocator() string { return t.remote }

package session

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
)

type tcpTransport struct {
	conn    net.Conn
	r       *bufio.Reader
	remote  string
	writeMu sync.Mutex
}

func dialTCP(ctx context.Context, addr string) (Transport, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	return &tcpTransport{
		conn:   conn,
		r:      bufio.NewReaderSize(conn, 65536),
		remote: "tcp/" + addr,
	}, nil
}

// ReadFrame reads a u16 little-endian length prefix followed by the frame bytes.
func (t *tcpTransport) ReadFrame() ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(t.r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint16(hdr[:])
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(t.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteFrame writes a u16 little-endian length prefix and then the frame bytes
// as a single contiguous write to avoid interleaving between goroutines.
func (t *tcpTransport) WriteFrame(data []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	buf := make([]byte, 2+len(data))
	binary.LittleEndian.PutUint16(buf[:2], uint16(len(data)))
	copy(buf[2:], data)
	_, err := t.conn.Write(buf)
	return err
}

func (t *tcpTransport) Close() error          { return t.conn.Close() }
func (t *tcpTransport) RemoteLocator() string { return t.remote }

package zenoh

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/tasanakorn/zenoh-nano-go/internal/wire"
	"go.uber.org/goleak"
)

// mockTransport is an in-memory session.Transport for unit tests.
type mockTransport struct {
	mu        sync.Mutex
	writes    [][]byte
	writesCh  chan struct{} // signals any new write
	readCh    chan []byte
	closeOnce sync.Once
	closed    bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		writesCh: make(chan struct{}, 1024),
		readCh:   make(chan []byte, 64),
	}
}

func (m *mockTransport) WriteFrame(data []byte) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("mock: closed")
	}
	// Copy to decouple from caller buffer.
	cp := make([]byte, len(data))
	copy(cp, data)
	m.writes = append(m.writes, cp)
	m.mu.Unlock()
	select {
	case m.writesCh <- struct{}{}:
	default:
	}
	return nil
}

func (m *mockTransport) ReadFrame() ([]byte, error) {
	b, ok := <-m.readCh
	if !ok {
		return nil, io.EOF
	}
	return b, nil
}

func (m *mockTransport) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		close(m.readCh)
	})
	return nil
}

func (m *mockTransport) RemoteLocator() string { return "test/mock" }

func (m *mockTransport) snapshotWrites() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.writes))
	copy(out, m.writes)
	return out
}

// newTestSession builds a Session with a mock transport and starts only writerLoop.
// No reader/keepalive/watchdog goroutines — tests call dispatchNetworkPayload directly.
func newTestSession(t *testing.T) (*Session, *mockTransport) {
	t.Helper()
	tr := newMockTransport()
	cfg := &Config{WriteQueueSize: 256}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		cfg:        cfg,
		zid:        NewRandomZID(),
		tr:         tr,
		ctx:        ctx,
		cancel:     cancel,
		writeCh:    make(chan []byte, cfg.WriteQueueSize),
		writerDone: make(chan struct{}),
		subs:       make(map[uint32]*Subscriber),
		queryables: make(map[uint32]*Queryable),
		queries:    make(map[uint64]*queryState),
		snMask:     ^uint64(0),
		batchSize:  wire.DefaultBatchSize,
		fragBufs:   make(map[fragKey][]byte),
	}
	s.lastRx.Store(time.Now().UnixNano())
	s.wg.Add(1)
	go s.writerLoop()
	return s, tr
}

// waitForWrites waits until at least n writes have occurred or times out.
func waitForWrites(t *testing.T, tr *mockTransport, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w := tr.snapshotWrites()
		if len(w) >= n {
			return w
		}
		select {
		case <-tr.writesCh:
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timeout waiting for %d writes, got %d", n, len(tr.snapshotWrites()))
	return nil
}

// decodeFrameNetworkMsgs decodes a raw transport frame bytes and returns the
// network messages from its payload.
func decodeFrameNetworkMsgs(t *testing.T, frame []byte) []wire.DecodedNetwork {
	t.Helper()
	msg, err := wire.DecodeTransport(frame)
	if err != nil {
		t.Fatalf("DecodeTransport: %v", err)
	}
	f, ok := msg.(*wire.Frame)
	if !ok {
		t.Fatalf("expected *wire.Frame, got %T", msg)
	}
	nms, err := wire.DecodeNetworkStream(f.Payload)
	if err != nil {
		t.Fatalf("DecodeNetworkStream: %v", err)
	}
	return nms
}

// findDeclareBody looks through all written frames for the first DECLARE message
// and returns its bodies.
func findDeclareBodies(t *testing.T, writes [][]byte) []wire.DeclareBody {
	t.Helper()
	for _, w := range writes {
		for _, nm := range decodeFrameNetworkMsgs(t, w) {
			if nm.Kind == wire.NidDeclare && nm.Declare != nil {
				return nm.Declare.Bodies
			}
		}
	}
	return nil
}

// collectAllNetworkMsgs decodes every write and returns all network messages.
func collectAllNetworkMsgs(t *testing.T, writes [][]byte) []wire.DecodedNetwork {
	t.Helper()
	var out []wire.DecodedNetwork
	for _, w := range writes {
		out = append(out, decodeFrameNetworkMsgs(t, w)...)
	}
	return out
}

func TestQueryableGoleak(t *testing.T) {
	// Verify no goroutines are leaked after a typical queryable lifecycle.
	defer goleak.VerifyNone(t)

	s, _ := newTestSession(t)
	qa, err := s.DeclareQueryable("test/**", func(q *Query) { _ = q.Close() })
	if err != nil {
		t.Fatalf("DeclareQueryable: %v", err)
	}
	if err := qa.Close(); err != nil {
		t.Fatalf("Queryable.Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Session.Close: %v", err)
	}
}

func TestDeclareQueryable(t *testing.T) {
	s, tr := newTestSession(t)
	defer s.Close()

	qa, err := s.DeclareQueryable("foo/**", func(q *Query) { _ = q.Close() })
	if err != nil {
		t.Fatalf("DeclareQueryable: %v", err)
	}
	if qa.KeyExpr() != "foo/**" {
		t.Fatalf("KeyExpr = %q, want %q", qa.KeyExpr(), "foo/**")
	}

	writes := waitForWrites(t, tr, 1)
	bodies := findDeclareBodies(t, writes)
	if bodies == nil {
		t.Fatalf("no DECLARE body found")
	}
	var found *wire.DeclQueryableBody
	for _, b := range bodies {
		if dq, ok := b.(*wire.DeclQueryableBody); ok {
			found = dq
			break
		}
	}
	if found == nil {
		t.Fatalf("no DeclQueryableBody in %+v", bodies)
	}
	if found.KeyExpr != "foo/**" {
		t.Fatalf("DeclQueryableBody.KeyExpr = %q, want %q", found.KeyExpr, "foo/**")
	}
	if found.QueryableID == 0 {
		t.Fatalf("QueryableID should not be 0")
	}
}

func TestQueryableUndeclare(t *testing.T) {
	s, tr := newTestSession(t)
	defer s.Close()

	qa, err := s.DeclareQueryable("bar/*", func(q *Query) { _ = q.Close() })
	if err != nil {
		t.Fatalf("DeclareQueryable: %v", err)
	}

	// Wait for initial DECLARE write.
	writes := waitForWrites(t, tr, 1)
	declBefore := len(writes)

	if err := qa.Undeclare(); err != nil {
		t.Fatalf("Undeclare: %v", err)
	}

	writes = waitForWrites(t, tr, declBefore+1)
	// Scan all network messages for an UndeclQueryableBody.
	var foundUndecl bool
	for _, nm := range collectAllNetworkMsgs(t, writes) {
		if nm.Kind != wire.NidDeclare || nm.Declare == nil {
			continue
		}
		for _, b := range nm.Declare.Bodies {
			if uq, ok := b.(*wire.UndeclQueryableBody); ok {
				if uq.QueryableID != qa.id {
					t.Fatalf("UndeclQueryableBody ID=%d, want %d", uq.QueryableID, qa.id)
				}
				foundUndecl = true
			}
		}
	}
	if !foundUndecl {
		t.Fatalf("no UndeclQueryableBody found")
	}
}

func TestQueryableHandlerInvoked(t *testing.T) {
	s, _ := newTestSession(t)
	defer s.Close()

	gotCh := make(chan *Query, 1)
	_, err := s.DeclareQueryable("svc/**", func(q *Query) {
		gotCh <- q
		_ = q.Close()
	})
	if err != nil {
		t.Fatalf("DeclareQueryable: %v", err)
	}

	// Inject a REQUEST with a QUERY body.
	reqID := uint64(42)
	body := wire.EncodeQueryBody(&wire.QueryBody{Parameters: "?x=1"})
	req := &wire.RequestMsg{RequestID: reqID, KeyExpr: "svc/math/add", Body: body}
	s.dispatchNetworkPayload(req.Encode())

	select {
	case q := <-gotCh:
		if q.KeyExpr() != "svc/math/add" {
			t.Fatalf("KeyExpr = %q, want %q", q.KeyExpr(), "svc/math/add")
		}
		if q.Parameters != "?x=1" {
			t.Fatalf("Parameters = %q, want %q", q.Parameters, "?x=1")
		}
		if q.requestID != reqID {
			t.Fatalf("requestID = %d, want %d", q.requestID, reqID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("handler not invoked")
	}
}

func TestQueryReplyClose(t *testing.T) {
	s, tr := newTestSession(t)
	defer s.Close()

	done := make(chan struct{})
	_, err := s.DeclareQueryable("q/**", func(q *Query) {
		defer close(done)
		if err := q.Reply("q/one", []byte("hello")); err != nil {
			t.Errorf("Reply: %v", err)
		}
		if err := q.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		// Second close is idempotent.
		if err := q.Close(); err != nil {
			t.Errorf("Close#2: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("DeclareQueryable: %v", err)
	}

	reqID := uint64(7)
	body := wire.EncodeQueryBody(&wire.QueryBody{})
	req := &wire.RequestMsg{RequestID: reqID, KeyExpr: "q/one", Body: body}
	s.dispatchNetworkPayload(req.Encode())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler did not complete")
	}

	// Wait until we see both RESPONSE and RESPONSE_FINAL across writes.
	deadline := time.Now().Add(2 * time.Second)
	var sawResponse, sawFinal int
	for time.Now().Before(deadline) {
		sawResponse, sawFinal = 0, 0
		for _, nm := range collectAllNetworkMsgs(t, tr.snapshotWrites()) {
			switch nm.Kind {
			case wire.NidResponse:
				if nm.Response != nil && nm.Response.RequestID == reqID {
					sawResponse++
				}
			case wire.NidResponseFinal:
				if nm.ResponseFinal != nil && nm.ResponseFinal.RequestID == reqID {
					sawFinal++
				}
			}
		}
		if sawResponse >= 1 && sawFinal >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sawResponse != 1 {
		t.Fatalf("RESPONSE count = %d, want 1", sawResponse)
	}
	if sawFinal != 1 {
		t.Fatalf("RESPONSE_FINAL count = %d, want 1 (second Close should be idempotent)", sawFinal)
	}

	// Verify RESPONSE payload.
	for _, nm := range collectAllNetworkMsgs(t, tr.snapshotWrites()) {
		if nm.Kind == wire.NidResponse && nm.Response != nil && nm.Response.RequestID == reqID {
			if nm.Response.Reply == nil {
				t.Fatalf("expected Reply body")
			}
			if !bytes.Equal(nm.Response.Reply.Payload, []byte("hello")) {
				t.Fatalf("reply payload = %q, want %q", nm.Response.Reply.Payload, "hello")
			}
			if nm.Response.KeyExpr != "q/one" {
				t.Fatalf("response KeyExpr = %q, want %q", nm.Response.KeyExpr, "q/one")
			}
		}
	}
}

func TestQueryAutoClose(t *testing.T) {
	s, tr := newTestSession(t)
	defer s.Close()

	done := make(chan struct{})
	_, err := s.DeclareQueryable("q/**", func(q *Query) {
		// Return without calling Close — dispatcher's defer should auto-close.
		close(done)
	})
	if err != nil {
		t.Fatalf("DeclareQueryable: %v", err)
	}

	reqID := uint64(99)
	body := wire.EncodeQueryBody(&wire.QueryBody{})
	req := &wire.RequestMsg{RequestID: reqID, KeyExpr: "q/auto", Body: body}
	s.dispatchNetworkPayload(req.Encode())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler did not run")
	}

	// Wait up to 2s for exactly one RESPONSE_FINAL for reqID.
	deadline := time.Now().Add(2 * time.Second)
	var finalCount int
	for time.Now().Before(deadline) {
		finalCount = 0
		for _, nm := range collectAllNetworkMsgs(t, tr.snapshotWrites()) {
			if nm.Kind == wire.NidResponseFinal && nm.ResponseFinal != nil && nm.ResponseFinal.RequestID == reqID {
				finalCount++
			}
		}
		if finalCount >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalCount != 1 {
		t.Fatalf("RESPONSE_FINAL count = %d, want 1", finalCount)
	}
}

func TestQueryNoMatch(t *testing.T) {
	s, tr := newTestSession(t)
	defer s.Close()

	// No queryables registered.
	reqID := uint64(123)
	body := wire.EncodeQueryBody(&wire.QueryBody{})
	req := &wire.RequestMsg{RequestID: reqID, KeyExpr: "unmatched/key", Body: body}
	s.dispatchNetworkPayload(req.Encode())

	// Wait until a RESPONSE_FINAL for reqID is observed.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		for _, nm := range collectAllNetworkMsgs(t, tr.snapshotWrites()) {
			if nm.Kind == wire.NidResponseFinal && nm.ResponseFinal != nil && nm.ResponseFinal.RequestID == reqID {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatalf("no RESPONSE_FINAL emitted for unmatched request %d", reqID)
	}
}

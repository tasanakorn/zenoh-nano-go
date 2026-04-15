package zenoh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tasanakorn/zenoh-nano-go/internal/kematch"
	"github.com/tasanakorn/zenoh-nano-go/internal/session"
	"github.com/tasanakorn/zenoh-nano-go/internal/wire"
)

// Sample is a value delivered to a subscriber.
type Sample struct {
	KeyExpr  string
	Payload  []byte
	Encoding Encoding
	Kind     SampleKind
}

// SampleKind distinguishes PUT and DELETE samples.
type SampleKind int

const (
	SampleKindPut SampleKind = iota
	SampleKindDelete
)

// Reply is one reply returned by Get.
type Reply struct {
	KeyExpr  string
	Payload  []byte
	Encoding Encoding
	Err      error // non-nil when reply carries an ERR body
}

// queryState tracks an in-flight Get.
type queryState struct {
	ch       chan Reply
	doneOnce sync.Once
}

// fragKey keys the fragment reassembly buffers by reliability class.
type fragKey struct{ reliable bool }

// Session is an open Zenoh client session.
type Session struct {
	cfg    *Config
	zid    ZenohID
	peerZI ZenohID

	tr session.Transport

	ctx    context.Context
	cancel context.CancelFunc

	writeCh      chan []byte
	writeChClose sync.Once  // guards close(writeCh)
	writerDone   chan struct{} // closed when writerLoop exits
	wg           sync.WaitGroup

	sendSN  atomic.Uint64 // monotonically-increasing, masked to snMask on send
	reqNext atomic.Uint64

	snMask    uint64 // SN modulus mask (based on negotiated resolution)
	batchSize uint16 // negotiated batch size (currently informational)

	lastRx atomic.Int64 // unix nanoseconds of last inbound transport message

	// fragBufs is only accessed from the reader goroutine.
	fragBufs map[fragKey][]byte

	mu              sync.Mutex
	subs            map[uint32]*Subscriber
	subIDNext       uint32
	queryables      map[uint32]*Queryable
	queryableIDNext uint32
	queries         map[uint64]*queryState
	closed          bool
	closeErr        error
}

// Open dials the first working locator in cfg.Connect and completes the handshake.
func Open(ctx context.Context, cfg *Config) (*Session, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if cfg.Lease == 0 {
		cfg.Lease = 10 * time.Second
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = 5 * time.Second
	}
	if cfg.WriteQueueSize == 0 {
		cfg.WriteQueueSize = 256
	}
	zid := cfg.ZID
	if zid.IsZero() {
		zid = NewRandomZID()
	}
	var (
		tr   session.Transport
		lerr error
	)
	hctx, cancelH := context.WithTimeout(ctx, cfg.HandshakeTimeout)
	defer cancelH()
	for _, loc := range cfg.Connect {
		t, err := session.DialTransport(hctx, loc)
		if err != nil {
			lerr = err
			continue
		}
		tr = t
		break
	}
	if tr == nil {
		if lerr == nil {
			lerr = errors.New("no locators provided")
		}
		return nil, wrapErr(ErrCatConnection, "open", lerr)
	}

	// Handshake.
	clientBatchSize := wire.DefaultBatchSize
	initSyn := &wire.InitSyn{
		WhatAmI:    wire.WhatAmIClientIdx,
		ZID:        zid.Bytes(),
		Resolution: wire.DefaultResolution,
		BatchSize:  clientBatchSize,
	}
	if err := tr.WriteFrame(initSyn.Encode()); err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatConnection, "write InitSyn", err)
	}
	frame, err := tr.ReadFrame()
	if err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatConnection, "read InitAck", err)
	}
	msg, err := wire.DecodeTransport(frame)
	if err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatProtocol, "decode InitAck", err)
	}
	ack, ok := msg.(*wire.InitAck)
	if !ok {
		tr.Close()
		return nil, wrapErr(ErrCatProtocol, "expected InitAck", fmt.Errorf("got %T", msg))
	}
	peerZID, err := ZIDFromBytes(ack.ZID)
	if err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatProtocol, "peer ZID", err)
	}

	// Apply negotiated parameters.
	agreedBatch := clientBatchSize
	if ack.BatchSize < agreedBatch {
		agreedBatch = ack.BatchSize
	}
	resolution := ack.Resolution & 0x03
	snBits := uint64(8) << resolution
	var snMask uint64
	if snBits >= 64 {
		snMask = ^uint64(0)
	} else {
		snMask = (uint64(1) << snBits) - 1
	}

	openSyn := &wire.OpenSyn{
		LeaseMs:   uint64(cfg.Lease / time.Millisecond),
		InitialSN: 0,
		Cookie:    ack.Cookie,
	}
	if err := tr.WriteFrame(openSyn.Encode()); err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatConnection, "write OpenSyn", err)
	}
	frame, err = tr.ReadFrame()
	if err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatConnection, "read OpenAck", err)
	}
	msg, err = wire.DecodeTransport(frame)
	if err != nil {
		tr.Close()
		return nil, wrapErr(ErrCatProtocol, "decode OpenAck", err)
	}
	if _, ok := msg.(*wire.OpenAck); !ok {
		tr.Close()
		return nil, wrapErr(ErrCatProtocol, "expected OpenAck", fmt.Errorf("got %T", msg))
	}

	sctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		cfg:       cfg,
		zid:       zid,
		peerZI:    peerZID,
		tr:        tr,
		ctx:       sctx,
		cancel:    cancel,
		writeCh:    make(chan []byte, cfg.WriteQueueSize),
		writerDone: make(chan struct{}),
		subs:       make(map[uint32]*Subscriber),
		queryables: make(map[uint32]*Queryable),
		queries:    make(map[uint64]*queryState),
		snMask:    snMask,
		batchSize: agreedBatch,
		fragBufs:  make(map[fragKey][]byte),
	}
	s.lastRx.Store(time.Now().UnixNano())
	s.wg.Add(2)
	go s.writerLoop()
	go s.readerLoop()
	if cfg.Lease > 0 {
		s.wg.Add(2)
		go s.keepAliveLoop()
		go s.watchdogLoop()
	}
	return s, nil
}

// Close closes the session and releases resources.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.closeErr
	}
	s.closed = true
	s.mu.Unlock()

	// Enqueue the CLOSE message so writerLoop sends it after draining pending writes.
	closeMsg := &wire.Close{Reason: 0, Session: true}
	_ = s.enqueue(closeMsg.Encode())

	// Closing writeCh causes writerLoop to drain all remaining messages
	// (including the CLOSE just enqueued) and then exit.
	s.writeChClose.Do(func() { close(s.writeCh) })

	// Wait for the writer to finish before closing the transport so that
	// all pending frames (including the CLOSE) are actually sent.
	<-s.writerDone

	// Now close the transport — this unblocks the reader goroutine.
	_ = s.tr.Close()

	// Cancel context to stop reader/keepalive/watchdog goroutines.
	s.cancel()

	// Drain queries.
	s.mu.Lock()
	for _, q := range s.queries {
		q.doneOnce.Do(func() { close(q.ch) })
	}
	s.queries = map[uint64]*queryState{}
	subs := s.subs
	s.subs = map[uint32]*Subscriber{}
	s.mu.Unlock()

	// Stop subscriber dispatch goroutines.
	for _, sub := range subs {
		sub.shutdown()
	}

	s.mu.Lock()
	qas := s.queryables
	s.queryables = map[uint32]*Queryable{}
	s.mu.Unlock()
	for _, qa := range qas {
		qa.shutdown()
	}

	s.wg.Wait()
	return nil
}

// ZID returns this session's Zenoh ID.
func (s *Session) ZID() ZenohID { return s.zid }

// PeerZID returns the remote peer's Zenoh ID.
func (s *Session) PeerZID() ZenohID { return s.peerZI }

// writerLoop serializes outbound network messages into FRAME envelopes and writes them.
// It exits when writeCh is closed, draining all pending messages first.
func (s *Session) writerLoop() {
	defer s.wg.Done()
	defer close(s.writerDone) // signal Close() that all writes are complete
	for payload := range s.writeCh {
		sn := s.sendSN.Add(1) & s.snMask
		frame := &wire.Frame{
			Reliable: true,
			SN:       sn,
			Payload:  payload,
		}
		if err := s.tr.WriteFrame(frame.Encode()); err != nil {
			s.setCloseErr(wrapErr(ErrCatConnection, "write", err))
			// Drain remaining to unblock any callers blocked in enqueue.
			for range s.writeCh {
			}
			return
		}
	}
}

// keepAliveLoop sends periodic KEEP_ALIVE messages.
func (s *Session) keepAliveLoop() {
	defer s.wg.Done()
	interval := s.cfg.Lease / 3
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	ka := (&wire.KeepAlive{}).Encode()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			if err := s.tr.WriteFrame(ka); err != nil {
				s.setCloseErr(wrapErr(ErrCatConnection, "keepalive", err))
				return
			}
		}
	}
}

// watchdogLoop monitors inbound traffic and closes the session if the peer
// stops responding for longer than 3 * lease.
func (s *Session) watchdogLoop() {
	defer s.wg.Done()
	interval := s.cfg.Lease
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			last := time.Unix(0, s.lastRx.Load())
			if time.Since(last) > 3*s.cfg.Lease {
				s.setCloseErr(ErrTimeout)
				return
			}
		}
	}
}

// readerLoop reads frames and dispatches network messages.
func (s *Session) readerLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		frame, err := s.tr.ReadFrame()
		if err != nil {
			if err != io.EOF {
				s.setCloseErr(wrapErr(ErrCatConnection, "read", err))
			}
			return
		}
		s.lastRx.Store(time.Now().UnixNano())
		if len(frame) == 0 {
			continue
		}
		s.dispatchTransport(frame)
	}
}

func (s *Session) dispatchTransport(frame []byte) {
	msg, err := wire.DecodeTransport(frame)
	if err != nil {
		log.Printf("zenoh: decode transport: %v", err)
		return
	}
	switch m := msg.(type) {
	case *wire.Frame:
		s.dispatchNetworkPayload(m.Payload)
	case *wire.KeepAlive:
		// ignore
	case *wire.Close:
		s.setCloseErr(wrapErr(ErrCatClosed, "peer close", fmt.Errorf("reason=%d", m.Reason)))
		s.cancel()
	case *wire.Fragment:
		key := fragKey{reliable: m.Reliable}
		s.fragBufs[key] = append(s.fragBufs[key], m.Payload...)
		if !m.More {
			payload := s.fragBufs[key]
			delete(s.fragBufs, key)
			s.dispatchNetworkPayload(payload)
		}
	default:
		// e.g. unsolicited InitAck/OpenAck -- ignore
	}
}

func (s *Session) dispatchNetworkPayload(payload []byte) {
	msgs, err := wire.DecodeNetworkStream(payload)
	if err != nil {
		log.Printf("zenoh: decode network stream: %v", err)
		// Still dispatch any successfully decoded prefix.
	}
	for _, nm := range msgs {
		switch nm.Kind {
		case wire.NidPush:
			if nm.Push != nil {
				s.handlePush(nm.Push)
			}
		case wire.NidRequest:
			if nm.Request != nil {
				s.handleRequest(nm.Request)
			}
		case wire.NidDeclare:
			// DECL_FINAL and similar -- ignore for now.
		case wire.NidResponse:
			if nm.Response != nil {
				s.handleResponse(nm.Response)
			}
		case wire.NidResponseFinal:
			if nm.ResponseFinal != nil {
				s.handleResponseFinal(nm.ResponseFinal)
			}
		}
	}
}

func (s *Session) handlePush(p *wire.DecodedPush) {
	if p.KeyExpr == "" {
		return
	}
	var sample Sample
	sample.KeyExpr = p.KeyExpr
	if p.Put != nil {
		sample.Payload = p.Put.Payload
		sample.Encoding = Encoding{ID: p.Put.EncodingID, Schema: p.Put.EncodingSchema}
		sample.Kind = SampleKindPut
	} else if p.Del != nil {
		sample.Kind = SampleKindDelete
	} else {
		return
	}
	s.mu.Lock()
	subs := make([]*Subscriber, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		if kematch.Match(sub.pattern, sample.KeyExpr) {
			select {
			case sub.ch <- sample:
			default:
				log.Printf("zenoh: slow subscriber %d, dropping sample for %q", sub.id, sample.KeyExpr)
			}
		}
	}
}

func (s *Session) handleRequest(req *wire.DecodedRequest) {
	if req.Query == nil || req.KeyExpr == "" {
		_ = s.enqueue((&wire.ResponseFinalMsg{RequestID: req.RequestID}).Encode())
		return
	}
	q := &Query{
		session:    s,
		requestID:  req.RequestID,
		keyExpr:    req.KeyExpr,
		Parameters: req.Query.Parameters,
		Payload:    req.Query.Payload,
		Encoding:   Encoding{ID: req.Query.EncodingID, Schema: req.Query.EncodingSchema},
	}
	s.mu.Lock()
	qs := make([]*Queryable, 0, len(s.queryables))
	for _, qa := range s.queryables {
		qs = append(qs, qa)
	}
	s.mu.Unlock()
	// Dispatch to the first matching queryable (first-match routing: one query
	// produces exactly one RESPONSE_FINAL from this session).
	for _, qa := range qs {
		if kematch.Match(qa.pattern, req.KeyExpr) {
			select {
			case qa.ch <- q:
				return
			default:
				// Queryable channel full — mark query closed and terminate immediately
				// so the requester's Get unblocks. q is not handed off anywhere so
				// the closed guard on Query is set here for correctness.
				q.mu.Lock()
				q.closed = true
				q.mu.Unlock()
				log.Printf("zenoh: slow queryable %d, dropping query for %q", qa.id, req.KeyExpr)
				_ = s.enqueue((&wire.ResponseFinalMsg{RequestID: req.RequestID}).Encode())
				return
			}
		}
	}
	// No matching queryable — terminate request so the caller's Get unblocks.
	_ = s.enqueue((&wire.ResponseFinalMsg{RequestID: req.RequestID}).Encode())
}

func (s *Session) handleResponse(resp *wire.DecodedResponse) {
	s.mu.Lock()
	q := s.queries[resp.RequestID]
	s.mu.Unlock()
	if q == nil {
		return
	}
	reply := Reply{KeyExpr: resp.KeyExpr}
	switch {
	case resp.Reply != nil:
		reply.Payload = resp.Reply.Payload
		reply.Encoding = Encoding{ID: resp.Reply.EncodingID, Schema: resp.Reply.EncodingSchema}
	case resp.Put != nil:
		reply.Payload = resp.Put.Payload
		reply.Encoding = Encoding{ID: resp.Put.EncodingID, Schema: resp.Put.EncodingSchema}
	case resp.Err != nil:
		reply.Err = fmt.Errorf("zenoh reply error")
		reply.Payload = resp.Err.Payload
		reply.Encoding = Encoding{ID: resp.Err.EncodingID, Schema: resp.Err.EncodingSchema}
	}
	select {
	case q.ch <- reply:
	default:
		log.Printf("zenoh: reply channel full for request %d, dropping reply", resp.RequestID)
	}
}

func (s *Session) handleResponseFinal(rf *wire.DecodedResponseFinal) {
	s.mu.Lock()
	q := s.queries[rf.RequestID]
	delete(s.queries, rf.RequestID)
	s.mu.Unlock()
	if q != nil {
		q.doneOnce.Do(func() { close(q.ch) })
	}
}

func (s *Session) setCloseErr(err error) {
	s.mu.Lock()
	if s.closeErr == nil {
		s.closeErr = err
	}
	s.closed = true
	s.mu.Unlock()
	s.writeChClose.Do(func() { close(s.writeCh) })
	s.cancel()
}

// enqueue a raw network message payload for the writer.
// Blocks until delivered or session is closed.
func (s *Session) enqueue(payload []byte) error {
	select {
	case <-s.ctx.Done():
		return ErrSessionClosed
	case s.writeCh <- payload:
		return nil
	}
}

// Put publishes a value under keyExpr.
func (s *Session) Put(keyExpr string, payload []byte) error {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return err
	}
	body := wire.EncodePutBody(&wire.PutBody{Payload: payload})
	push := &wire.PushMsg{KeyExpr: keyExpr, Body: body}
	return s.enqueue(push.Encode())
}

// Delete publishes a DELETE for keyExpr.
func (s *Session) Delete(keyExpr string) error {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return err
	}
	body := wire.EncodeDelBody(&wire.DelBody{})
	push := &wire.PushMsg{KeyExpr: keyExpr, Body: body}
	return s.enqueue(push.Encode())
}

// DeclareSubscriber registers cb to be invoked for samples matching keyExpr.
func (s *Session) DeclareSubscriber(keyExpr string, cb func(Sample)) (*Subscriber, error) {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return nil, err
	}
	if cb == nil {
		return nil, wrapErr(ErrCatInvalidArg, "subscriber callback", errors.New("nil callback"))
	}
	s.mu.Lock()
	s.subIDNext++
	id := s.subIDNext
	sub := &Subscriber{
		session: s,
		id:      id,
		keyExpr: keyExpr,
		pattern: keyExpr,
		cb:      cb,
		ch:      make(chan Sample, 64),
		done:    make(chan struct{}),
	}
	s.subs[id] = sub
	s.mu.Unlock()

	go sub.dispatch()

	decl := &wire.DeclareMsg{Bodies: []wire.DeclareBody{
		&wire.DeclSubscriberBody{SubID: id, KeyExpr: keyExpr},
	}}
	if err := s.enqueue(decl.Encode()); err != nil {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
		sub.shutdown()
		return nil, err
	}
	return sub, nil
}

// undeclareSubscriber removes a subscriber.
func (s *Session) undeclareSubscriber(id uint32) error {
	s.mu.Lock()
	sub, ok := s.subs[id]
	delete(s.subs, id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	decl := &wire.DeclareMsg{Bodies: []wire.DeclareBody{
		&wire.UndeclSubscriberBody{SubID: id},
	}}
	err := s.enqueue(decl.Encode())
	sub.shutdown()
	return err
}

// DeclareQueryable registers handler to be called for incoming queries matching keyExpr.
// The returned Queryable must be closed via Queryable.Close or Session.Close.
func (s *Session) DeclareQueryable(keyExpr string, handler func(*Query)) (*Queryable, error) {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, wrapErr(ErrCatInvalidArg, "queryable handler", errors.New("nil handler"))
	}
	s.mu.Lock()
	s.queryableIDNext++
	id := s.queryableIDNext
	qa := &Queryable{
		session: s,
		id:      id,
		keyExpr: keyExpr,
		pattern: keyExpr,
		handler: handler,
		ch:      make(chan *Query, 64),
		done:    make(chan struct{}),
	}
	s.queryables[id] = qa
	s.mu.Unlock()

	go qa.dispatch()

	decl := &wire.DeclareMsg{Bodies: []wire.DeclareBody{
		&wire.DeclQueryableBody{QueryableID: id, KeyExpr: keyExpr},
	}}
	if err := s.enqueue(decl.Encode()); err != nil {
		s.mu.Lock()
		delete(s.queryables, id)
		s.mu.Unlock()
		qa.shutdown()
		return nil, err
	}
	return qa, nil
}

func (s *Session) undeclareQueryable(id uint32) error {
	s.mu.Lock()
	qa, ok := s.queryables[id]
	delete(s.queryables, id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	decl := &wire.DeclareMsg{Bodies: []wire.DeclareBody{
		&wire.UndeclQueryableBody{QueryableID: id},
	}}
	err := s.enqueue(decl.Encode())
	qa.shutdown()
	return err
}

// Get issues a query for selector and returns the collected replies.
// The query terminates when the peer sends RESPONSE_FINAL or ctx is done.
func (s *Session) Get(ctx context.Context, selector string) ([]Reply, error) {
	ke, params := SplitSelector(selector)
	if err := ValidateKeyExpr(ke); err != nil {
		return nil, err
	}
	reqID := s.reqNext.Add(1)
	q := &queryState{ch: make(chan Reply, 256)}
	s.mu.Lock()
	s.queries[reqID] = q
	s.mu.Unlock()

	body := wire.EncodeQueryBody(&wire.QueryBody{Parameters: params})
	req := &wire.RequestMsg{
		RequestID: reqID,
		KeyExpr:   ke,
		Body:      body,
	}
	if err := s.enqueue(req.Encode()); err != nil {
		s.mu.Lock()
		delete(s.queries, reqID)
		s.mu.Unlock()
		return nil, err
	}

	var out []Reply
	for {
		select {
		case r, ok := <-q.ch:
			if !ok {
				return out, nil
			}
			out = append(out, r)
		case <-ctx.Done():
			s.mu.Lock()
			delete(s.queries, reqID)
			s.mu.Unlock()
			return out, ctx.Err()
		case <-s.ctx.Done():
			return out, ErrSessionClosed
		}
	}
}

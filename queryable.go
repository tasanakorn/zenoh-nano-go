package zenoh

import (
	"log"
	"sync"

	"github.com/tasanakorn/zenoh-nano-go/internal/wire"
)

// ReplyOption configures a single Reply call.
type ReplyOption func(*replyOptions)

type replyOptions struct {
	encoding Encoding
}

// WithReplyEncoding sets the encoding for one reply.
func WithReplyEncoding(enc Encoding) ReplyOption {
	return func(o *replyOptions) { o.encoding = enc }
}

// Query is an inbound query routed to a Queryable handler.
// Not safe for concurrent use from multiple goroutines.
type Query struct {
	session   *Session
	requestID uint64
	keyExpr   string

	// Parameters is the selector parameter string (may be empty).
	Parameters string
	// Payload is the optional query payload (may be nil).
	Payload []byte
	// Encoding is the encoding of Payload.
	Encoding Encoding

	mu       sync.Mutex
	closed   bool
	closeErr error
}

// KeyExpr returns the key expression this query targeted.
func (q *Query) KeyExpr() string { return q.keyExpr }

// Reply emits one RESPONSE{REPLY} frame back to the requester.
// May be called multiple times before Close.
func (q *Query) Reply(keyExpr string, payload []byte, opts ...ReplyOption) error {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return err
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrSessionClosed
	}
	q.mu.Unlock()
	o := replyOptions{}
	for _, f := range opts {
		f(&o)
	}
	body := wire.EncodeReplyBody(&wire.ReplyBody{
		EncodingID:     o.encoding.ID,
		EncodingSchema: o.encoding.Schema,
		Payload:        payload,
	})
	msg := &wire.ResponseMsg{RequestID: q.requestID, KeyExpr: keyExpr, Body: body}
	return q.session.enqueue(msg.Encode())
}

// ReplyErr emits one RESPONSE{ERR} frame.
func (q *Query) ReplyErr(payload []byte) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrSessionClosed
	}
	q.mu.Unlock()
	body := wire.EncodeErrBody(&wire.ErrBody{Payload: payload})
	msg := &wire.ResponseMsg{RequestID: q.requestID, KeyExpr: q.keyExpr, Body: body}
	return q.session.enqueue(msg.Encode())
}

// Close emits RESPONSE_FINAL, terminating the query response stream.
// Idempotent; subsequent calls return nil.
func (q *Query) Close() error {
	q.mu.Lock()
	if q.closed {
		err := q.closeErr
		q.mu.Unlock()
		return err
	}
	q.closed = true
	q.mu.Unlock()
	msg := &wire.ResponseFinalMsg{RequestID: q.requestID}
	err := q.session.enqueue(msg.Encode())
	q.mu.Lock()
	q.closeErr = err
	q.mu.Unlock()
	return err
}

// Queryable is a registered server-side query handler.
// Obtain via Session.DeclareQueryable. Release via Queryable.Close.
type Queryable struct {
	session *Session
	id      uint32
	keyExpr string
	pattern string
	handler func(*Query)
	ch      chan *Query
	done    chan struct{}

	closeOnce sync.Once
}

// KeyExpr returns the key expression this queryable is registered on.
func (q *Queryable) KeyExpr() string { return q.keyExpr }

// dispatch is the per-queryable goroutine that calls the handler for each Query.
func (q *Queryable) dispatch() {
	defer close(q.done)
	for query := range q.ch {
		func(query *Query) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("zenoh: queryable handler panic: %v", r)
				}
				// Auto-close: if handler didn't call Close, emit RESPONSE_FINAL.
				// If handler already closed, sync.Mutex makes this a no-op.
				_ = query.Close()
			}()
			q.handler(query)
		}(query)
	}
}

func (q *Queryable) shutdown() {
	if q == nil {
		return
	}
	q.closeOnce.Do(func() { close(q.ch) })
	<-q.done
}

// Undeclare removes the queryable from the session and sends UNDECL_QUERYABLE.
func (q *Queryable) Undeclare() error {
	if q == nil || q.session == nil {
		return nil
	}
	return q.session.undeclareQueryable(q.id)
}

// Close is an alias for Undeclare.
func (q *Queryable) Close() error { return q.Undeclare() }

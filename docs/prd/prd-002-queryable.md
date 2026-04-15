# PRD-002: Implement Queryable

| Field   | Value                                             |
|---------|---------------------------------------------------|
| Status  | Implemented (v0.2.0)                              |
| Version | v0.2.0                                            |
| Author  | Tasanakorn (design) + Claude Code (PRD authoring) |
| Package | github.com/tasanakorn/zenoh-nano-go               |

## Goals

- Add a public `Session.DeclareQueryable(keyExpr string, handler func(Query)) (*Queryable, error)` API so a `zenoh-nano-go` client can serve `Get` requests issued by other Zenoh entities.
- Correctly **decode inbound `REQUEST { QUERY }`** network messages routed to this client by `zenohd`, match the query key expression against registered queryables using the same wildcard logic as subscribers, and dispatch to the user-provided handler on a per-queryable goroutine.
- Give the handler a `Query` object that exposes the request's key expression, parameters, and optional payload, plus `Reply(payload []byte, opts ...ReplyOption) error`, `ReplyErr(msg string) error`, and `Close() error` methods that emit `RESPONSE { REPLY }`, `RESPONSE { ERR }`, and `RESPONSE_FINAL` frames back to the router.
- Fix the silent correctness bug in `DecodeNetworkStream` where `io.ReadAll` on `NidRequest` drains the reader and loses any subsequent network messages batched in the same frame.
- Round-trip interoperate with `zenohd` v1.8 and with `eclipse-zenoh` Python clients issuing `session.get(...)` against a queryable hosted by `zenoh-nano-go`.

## Non-goals

- **Consolidation modes on outbound replies.** The queryable always emits replies verbatim; consolidation (`None`, `Monotonic`, `Latest`) is a router-side concern and is not expressed on our wire side for v0.2.0.
- **Queryable pull mode.** Only push-style replies (server sends replies eagerly in the handler, then `Close()`) are supported.
- **Admin space queries.** Queries addressed to `@/router/*` / `@/session/*` are not routed to user queryables.
- **Multi-session queryable fanout.** A `Queryable` is bound to the `Session` on which it was declared. There is no routing layer that forwards queries across sessions.
- **Timestamp extension on replies.** Replies are emitted without a timestamp body flag; the router is free to stamp forwarded replies on the far side.
- **Liveliness / token integration.** Liveliness is out of scope for the whole library (see PRD-001) and this PRD does not expand it.

## Background & Motivation

Zenoh's query/queryable pair is the request/response half of the protocol: a client calls `session.Get(selector)` to issue a `REQUEST { QUERY }`, and any entity that has previously declared a **Queryable** matching the selector's key expression receives that query and emits zero-or-more `RESPONSE { REPLY }` frames plus a terminal `RESPONSE_FINAL`. PRD-001 shipped the *client side* of this flow: `Session.Get` in `querier.go` sends `REQUEST { QUERY }`, registers a reply channel, and consumes `RESPONSE` / `RESPONSE_FINAL`.

The *server side* — declaring a queryable, receiving incoming queries, and replying — is absent. This PRD closes that gap for v0.2.0.

### Current state

- **`querier.go`** implements `Session.Get` using the request correlator (`s.queries` map keyed by outbound `RequestID`). This path works end-to-end against `zenohd`.
- **`internal/wire/network_msg.go:291-296`** has this hazard for `NidRequest`, `NidInterest`, and `NidOAM`:

  ```go
  case NidInterest, NidOAM, NidRequest:
      // Ignored / not expected on client. Without explicit length, we cannot
      // skip past a message of this kind safely within a batched frame, so
      // treat any remaining bytes as consumed.
      _, _ = io.ReadAll(r)
      return out, nil
  ```

  The moment `zenohd` batches a `REQUEST` together with any following network message into a single `Frame`, that following message is silently dropped. Once we begin receiving `REQUEST { QUERY }` frames this turns into dropped declarations, dropped pushes, or dropped response-finals.
- **`internal/wire/body.go:227-232`** has a stub decoder for `ZidQuery`:

  ```go
  case ZidQuery:
      _, _ = bytes.NewReader(nil), fmt.Sprintf("")
      _, _ = io.ReadAll(r)
      return &QueryBody{}, mid, nil
  ```

  The returned `QueryBody` is always empty — parameters, encoding, and payload are discarded.
- **`internal/wire/body.go`** has `EncodePutBody`, `EncodeDelBody`, `EncodeQueryBody`, but **no `EncodeReplyBody`** and no `EncodeErrBody`. The wire struct `ReplyBody` exists and `DecodeReplyBody` exists (used by `Get`), but the send side is missing.
- **`internal/wire/network_msg.go`** exposes `RequestMsg.Encode()` (send side, used by `Get`) and `DecodeResponse` / `DecodeResponseFinal` (receive side, used by `Get`), but has **no `DecodeRequest` / `DecodedRequest`** (receive side for a queryable) and **no `ResponseMsg.Encode()` / `ResponseFinalMsg.Encode()`** (send side for replies).
- **`internal/wire/declare.go`** implements `DeclKeyExprBody`, `UndeclKeyExprBody`, `DeclSubscriberBody`, `UndeclSubscriberBody`, `DeclFinalBody`. The header IDs `DeclQueryable = 0x04` and `UndeclQue = 0x05` are defined in `internal/wire/header.go` but **no `DeclQueryableBody` / `UndeclQueryableBody` types, encoders, or decoders** exist.
- **`session.go:398-422`** `dispatchNetworkPayload` has switch cases for `NidPush`, `NidDeclare`, `NidResponse`, `NidResponseFinal`. There is **no `NidRequest` case** because inbound requests never decode today (see the hazard above).
- **`subscriber.go`** is the canonical model for a declared entity with a dispatch goroutine, buffered channel, `closeOnce`, and session-wide map keyed by a monotonic `uint32` ID. The queryable plumbing in this PRD follows that pattern exactly.

## Design

Implementation is split across three layers and follows the same structure PRD-001 established.

### Wire codec additions (`internal/wire/`)

Four wire-level concerns are added, all in `internal/wire`. Each gets a round-trip unit test alongside its implementation.

**1. `DecodedRequest` + `DecodeRequest` + batched-frame integration (`internal/wire/network_msg.go`).**
Mirror `DecodedResponse` exactly:

```go
// DecodedRequest is the decoded form of a REQUEST network message.
type DecodedRequest struct {
    RequestID  uint64
    DeclaredID uint16
    KeyExpr    string   // empty if DeclaredID refers to an interned expression
    BodyMID    uint8    // always ZidQuery for queryable dispatch
    Query      *QueryBody
}

// DecodeRequest decodes a REQUEST network message from an exact-length slice.
func DecodeRequest(data []byte, flags uint8) (*DecodedRequest, error)

// decodeRequestFromReader decodes a REQUEST from a bufio.Reader inside a
// batched Frame payload. The body is self-delimiting only via the Query
// body's own structure, so the reader MUST consume exactly the bytes that
// belong to this REQUEST and no more.
func decodeRequestFromReader(r *bufio.Reader, flags uint8) (*DecodedRequest, error)
```

Layout mirrors `RequestMsg.Encode()` byte-for-byte:

```
header(NidRequest, flags)
request_id   : uvarint
wire_expr    : uvarint declared_id [+ zstring suffix if FlagN]
extensions   : if FlagZ is set
body         : one body starting with ZidQuery header byte
```

**Stream consumption.** The body's trailing payload (if any) is a single `ZBytes` at the end of the REQUEST; `ReadZBytes` consumes exactly its varint-prefixed length, so the reader position lands on the next network message. Replace the `NidInterest, NidOAM, NidRequest` case in `DecodeNetworkStream` with:

```go
case NidRequest:
    req, err := decodeRequestFromReader(r, flags)
    if err != nil {
        return out, err
    }
    out = append(out, DecodedNetwork{Kind: NidRequest, Request: req})
case NidInterest, NidOAM:
    // Still not supported on the client. Without a length prefix we cannot
    // skip them inside a batched frame; drain and stop.
    _, _ = io.ReadAll(r)
    return out, nil
```

Add a `Request *DecodedRequest` field to `DecodedNetwork`.

**2. `DecodeQueryBody` (`internal/wire/body.go`).**
Replace the stub with a real decoder that consumes exactly the query body:

```go
func DecodeQueryBody(r *bufio.Reader, flags uint8) (*QueryBody, error) {
    hasZ := flags&FlagZ != 0
    const flagParams = uint8(0x20) // Q flag = parameters present
    b := &QueryBody{}
    if flags&flagParams != 0 {
        p, err := ReadZString(r)
        if err != nil {
            return nil, err
        }
        b.Parameters = p
    }
    if err := SkipExtensions(r, hasZ); err != nil {
        return nil, err
    }
    // Optional body: encoding + payload, present iff more bytes remain.
    if _, perr := r.Peek(1); perr == nil {
        id, schema, err := ReadEncoding(r)
        if err != nil {
            return nil, err
        }
        b.EncodingID = id
        b.EncodingSchema = schema
        payload, err := ReadZBytes(r)
        if err != nil && err != io.EOF {
            return nil, err
        }
        b.Payload = payload
        b.HasPayload = true
    }
    return b, nil
}
```

Wire it into `DecodeBody`'s `ZidQuery` case, replacing the current drain-and-return-empty stub.

**3. `EncodeReplyBody` + `EncodeErrBody` (`internal/wire/body.go`).**

```go
func EncodeReplyBody(b *ReplyBody) []byte {
    var buf []byte
    var flags uint8
    hasEnc := b.EncodingID != 0 || b.EncodingSchema != ""
    if hasEnc {
        flags |= BodyFlagI
    }
    buf = append(buf, MakeHeader(ZidReply, flags))
    if hasEnc {
        buf = AppendEncoding(buf, b.EncodingID, b.EncodingSchema)
    }
    buf = AppendZBytes(buf, b.Payload)
    return buf
}

func EncodeErrBody(b *ErrBody) []byte {
    var buf []byte
    var flags uint8
    hasEnc := b.EncodingID != 0 || b.EncodingSchema != ""
    if hasEnc {
        flags |= BodyFlagI
    }
    buf = append(buf, MakeHeader(ZidErr, flags))
    if hasEnc {
        buf = AppendEncoding(buf, b.EncodingID, b.EncodingSchema)
    }
    buf = AppendZBytes(buf, b.Payload)
    return buf
}
```

Shape matches `EncodePutBody`: header byte, optional encoding, zbytes payload, no timestamp, no extensions.

**4. `ResponseMsg` + `ResponseFinalMsg` (`internal/wire/network_msg.go`).**

```go
// ResponseMsg is a network RESPONSE message (emitted by a queryable handler).
type ResponseMsg struct {
    RequestID  uint64
    DeclaredID uint16
    KeyExpr    string
    Body       []byte // encoded body (includes body header)
}

func (m *ResponseMsg) Encode() []byte {
    var buf []byte
    var flags uint8
    if m.KeyExpr != "" {
        flags |= FlagN
    }
    buf = append(buf, MakeHeader(NidResponse, flags))
    buf = AppendUvarint(buf, m.RequestID)
    buf = AppendUvarint(buf, uint64(m.DeclaredID))
    if m.KeyExpr != "" {
        buf = AppendZString(buf, m.KeyExpr)
    }
    buf = append(buf, m.Body...)
    return buf
}

// ResponseFinalMsg is a network RESPONSE_FINAL message terminating a query.
type ResponseFinalMsg struct {
    RequestID uint64
}

func (m *ResponseFinalMsg) Encode() []byte {
    var buf []byte
    buf = append(buf, MakeHeader(NidResponseFinal, 0))
    buf = AppendUvarint(buf, m.RequestID)
    return buf
}
```

Shape is the inverse of `decodeResponseFromReader` / `decodeResponseFinalFromReader` and mirrors `RequestMsg.Encode()`.

**5. `DeclQueryableBody` + `UndeclQueryableBody` (`internal/wire/declare.go`).**
Mirror `DeclSubscriberBody` / `UndeclSubscriberBody` exactly. Header IDs (`DeclQueryable = 0x04`, `UndeclQue = 0x05`) are already defined in `header.go`.

```go
type DeclQueryableBody struct {
    QueryableID uint32
    DeclaredID  uint16
    KeyExpr     string
}
func (*DeclQueryableBody) declareBodyID() uint8 { return DeclQueryable }

type UndeclQueryableBody struct {
    QueryableID uint32
}
func (*UndeclQueryableBody) declareBodyID() uint8 { return UndeclQue }
```

Extend `EncodeDeclareBody` and `DecodeDeclareBody` with the two new cases. Encoders match subscriber encoders, substituting the new IDs.

### Public API (`queryable.go`, `session.go`)

New file `queryable.go` is modelled one-for-one on `subscriber.go`.

```go
package zenoh

import (
    "log"
    "sync"
)

// Query is the inbound view of a REQUEST { QUERY } routed to a Queryable.
// Query is NOT safe for concurrent use from multiple goroutines; the handler
// owns it for the duration of the handler invocation and must call Close when
// finished (Close is idempotent).
type Query struct {
    session   *Session
    requestID uint64
    keyExpr   string

    Parameters string
    Payload    []byte
    Encoding   Encoding

    closeOnce sync.Once
    closeErr  error
}

func (q *Query) KeyExpr() string { return q.keyExpr }

// Reply emits one RESPONSE { REPLY } frame carrying payload back to the
// requester. It is safe to call Reply multiple times before Close.
func (q *Query) Reply(keyExpr string, payload []byte, opts ...ReplyOption) error

// ReplyErr emits one RESPONSE { ERR } frame.
func (q *Query) ReplyErr(payload []byte) error

// Close emits RESPONSE_FINAL terminating this query. It is idempotent and is
// also called automatically if the handler returns without calling Close.
func (q *Query) Close() error

// Queryable is a registered queryable handle.
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

func (q *Queryable) KeyExpr() string { return q.keyExpr }
func (q *Queryable) Undeclare() error
func (q *Queryable) Close() error { return q.Undeclare() }
```

`ReplyOption` covers optional encoding on a per-reply basis:

```go
type ReplyOption func(*replyOptions)
type replyOptions struct {
    encoding Encoding
}
func WithReplyEncoding(enc Encoding) ReplyOption
```

Handler signature: `func(*Query)`. Pointer receiver matches `Subscriber`'s `func(Sample)` in spirit but carries state (the response lifecycle is mutable), so the pointer form is preferable to a value receiver.

**Auto-close contract.** The dispatch goroutine wraps every handler call with `defer q.Close()` (and a `recover()` that logs and still closes). If the handler returned without calling `Close`, the deferred `Close` emits `RESPONSE_FINAL`; if the handler already closed, the `sync.Once` in `Query.closeOnce` makes the deferred call a no-op. This guarantees the router always sees a `RESPONSE_FINAL`.

### Session integration (`session.go`)

Four sites change.

**1. State.** Add alongside the existing `subs` map:

```go
queryables     map[uint32]*Queryable
queryableIDNext uint32
```

Both are guarded by the existing `s.mu`.

**2. Declaration plumbing.** Add `DeclareQueryable` and `undeclareQueryable` as near-verbatim copies of `DeclareSubscriber` and `undeclareSubscriber` at `session.go:536-571` and `:574+`, substituting the new map, new wire body, and a dispatch channel whose element type is `*Query` (capacity 64, matching subscribers).

```go
func (s *Session) DeclareQueryable(keyExpr string, handler func(*Query)) (*Queryable, error)
```

**3. Inbound dispatch.** Add a `NidRequest` case to `dispatchNetworkPayload` at `session.go:404`:

```go
case wire.NidRequest:
    if nm.Request != nil {
        s.handleRequest(nm.Request)
    }
```

and a new `handleRequest` parallel to `handlePush`:

```go
func (s *Session) handleRequest(req *wire.DecodedRequest) {
    if req.Query == nil || req.KeyExpr == "" {
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
    queryables := make([]*Queryable, 0, len(s.queryables))
    for _, qa := range s.queryables {
        queryables = append(queryables, qa)
    }
    s.mu.Unlock()
    matched := false
    for _, qa := range queryables {
        if kematch.Match(qa.pattern, req.KeyExpr) {
            matched = true
            select {
            case qa.ch <- q:
            default:
                log.Printf("zenoh: slow queryable %d, dropping query for %q",
                    qa.id, req.KeyExpr)
                // A dropped query still needs RESPONSE_FINAL or the requester
                // hangs. Emit it synchronously from here.
                _ = s.enqueue((&wire.ResponseFinalMsg{RequestID: req.RequestID}).Encode())
            }
            // First match wins for single-session routing. (Multi-session
            // fanout is explicitly out of scope; see Non-goals.)
            break
        }
    }
    if !matched {
        // No queryable matches. Router expects SOMEONE to close the request;
        // if nobody does, the requester's Get blocks until its context
        // timeout. Emit RESPONSE_FINAL so the requester unblocks promptly.
        _ = s.enqueue((&wire.ResponseFinalMsg{RequestID: req.RequestID}).Encode())
    }
}
```

Routing note: unlike `handlePush`, which fans out to every matching subscriber, `handleRequest` delivers to the **first** matching queryable. A single request must produce exactly one `RESPONSE_FINAL` from this session; duplicating the request to N handlers would produce N `RESPONSE_FINAL`s and confuse the requester's correlator. If multiple registered queryables match, the handler routing is stable but unspecified (map iteration); callers who need multiple queryables for overlapping key expressions should disambiguate via the selector.

**4. Close.** Extend `Session.Close` to iterate `s.queryables`, call each queryable's `shutdown` (which closes the dispatch channel, letting in-flight handlers finish). Do **not** emit `UndeclQueryable` wire messages on `Close` — the transport is about to go away. This mirrors how `Close` handles subscribers today.

### RequestID namespace

Outbound request IDs (allocated by `Session.Get` in `querier.go`, stored in `s.queries`) and inbound request IDs (received from the router in `REQUEST { QUERY }`, stored in `Query.requestID`) live in separate namespaces. They never collide because:

- Outbound IDs are allocated by this client from `s.queryIDNext` and used as keys in `s.queries`.
- Inbound IDs are assigned by the requester (a remote entity) and only ever appear as the `RequestID` field in `ResponseMsg` / `ResponseFinalMsg` that this session emits in reply.

The two spaces never index the same map. No synchronization is required beyond what already exists.

### Key expression matching

Reuse `internal/kematch.Match(pattern, keyExpr)` exactly as `handlePush` does. Queryable declarations store the key expression as both `keyExpr` (human-facing) and `pattern` (match input), identical to `Subscriber`.

## Changes by Component

| Component                | Path                                       | Change                                                                                                                                                          |
|--------------------------|--------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Wire: query body decode  | `internal/wire/body.go`                    | Modify. Replace the `DecodeBody`/`ZidQuery` stub with a real `DecodeQueryBody` that reads parameters (Q flag 0x20), extensions, and optional encoding+payload.  |
| Wire: reply/err encode   | `internal/wire/body.go`                    | Add. `EncodeReplyBody`, `EncodeErrBody` mirroring `EncodePutBody`.                                                                                              |
| Wire: request decode     | `internal/wire/network_msg.go`             | Add. `DecodedRequest`, `DecodeRequest`, `decodeRequestFromReader`; add `Request *DecodedRequest` to `DecodedNetwork`; route `NidRequest` in `DecodeNetworkStream` instead of draining. |
| Wire: response encode    | `internal/wire/network_msg.go`             | Add. `ResponseMsg.Encode`, `ResponseFinalMsg.Encode`.                                                                                                           |
| Wire: declare queryable  | `internal/wire/declare.go`                 | Add. `DeclQueryableBody`, `UndeclQueryableBody`, extend `EncodeDeclareBody` and `DecodeDeclareBody`.                                                            |
| Wire: round-trip tests   | `internal/wire/*_test.go`                  | Add. Encode/decode round trips for `DecodeRequest`, `DecodeQueryBody`, `EncodeReplyBody`, `ResponseMsg`, `ResponseFinalMsg`, `DeclQueryableBody`, `UndeclQueryableBody`; golden byte vectors captured against live `zenohd`. |
| Public queryable API     | `queryable.go`                             | Add. `Query` type, `Queryable` type, `ReplyOption` / `WithReplyEncoding`, dispatch goroutine, `shutdown`.                                                       |
| Session: state           | `session.go`                               | Modify. Add `queryables` map and `queryableIDNext` counter alongside existing subscriber state; initialize in `Open`.                                           |
| Session: declare API     | `session.go`                               | Add. `DeclareQueryable(keyExpr, handler) (*Queryable, error)` and internal `undeclareQueryable(id)`.                                                            |
| Session: inbound handler | `session.go`                               | Add `NidRequest` case in `dispatchNetworkPayload`; add `handleRequest(req *wire.DecodedRequest)` function.                                                      |
| Session: close           | `session.go`                               | Modify `Close` to shut down live queryables alongside subscribers.                                                                                              |
| Example                  | `examples/z_queryable/`                    | Add. Minimal CLI that declares a queryable on a key expression and replies with a fixed payload, mirroring `examples/z_sub`.                                    |
| Python e2e tests         | `tests/python-e2e/`                        | Add. A test that uses `eclipse-zenoh` Python to call `session.get("demo/**")` against a `zenoh-nano-go` queryable; assert at least one reply and clean termination. |
| Docs                     | `docs/prd/prd-002-queryable.md`            | Add. This PRD.                                                                                                                                                  |
| Docs index               | `docs/README.md`                           | Modify. Add a row for PRD-002.                                                                                                                                  |

## Edge Cases

- **Batched-frame regression.** Today, any `Frame` payload that packs a `REQUEST` next to another network message silently drops everything after the `REQUEST`. Replacing the `io.ReadAll` drain with a real `decodeRequestFromReader` is a **correctness fix** even for clients that do not declare a queryable. Add a regression test: a hand-crafted `Frame` payload containing `REQUEST` followed by `PUSH` must yield both.
- **Query arrives with no matching queryable.** The router still expects the request to terminate. `handleRequest` emits `RESPONSE_FINAL` directly so the requester's `Get` unblocks instead of waiting for its context timeout.
- **Slow queryable handler.** The per-queryable dispatch channel has capacity 64, matching subscribers. If it is full, the new query is dropped and a `RESPONSE_FINAL` is emitted synchronously for that `RequestID` to keep the requester from hanging. Logged at warning level.
- **Handler panic.** The dispatch goroutine wraps the handler in `defer recover()`; a panic is logged, `Query.Close()` still fires via `defer`, and the queryable remains registered.
- **Handler returns without calling `Close`.** The dispatch goroutine's `defer q.Close()` emits `RESPONSE_FINAL`. The `sync.Once` makes this safe if the handler already closed.
- **Handler calls `Reply` after `Close`.** `Reply` returns `ErrSessionClosed`-like error; `Close` checks the `closeOnce` state before emitting further frames. No wire traffic is produced.
- **Multiple replies.** The handler may call `Reply` any number of times before `Close`. Each produces one `RESPONSE { REPLY }` frame with the same `RequestID`.
- **Session closed mid-handler.** `Session.Close` shuts the dispatch channel; the dispatch goroutine drains any queued queries and exits. `Reply` / `Close` calls from user code return `ErrSessionClosed` but do not panic.
- **Concurrent `DeclareQueryable` from many goroutines.** Protected by the existing `s.mu`; ID allocation is monotonic.
- **Declared ID (interned key expression) on a request.** `decodeRequestFromReader` already reads `(DeclaredID, KeyExpr)` via `ReadWireExpr`, matching the existing response path. Current `zenohd` practice is to send the full string form to clients; declared-ID form is handled if it appears but the runtime currently does not intern expressions (out of scope for this PRD).
- **Zero-length query payload.** `DecodeQueryBody` distinguishes "body absent" (no bytes after extensions) from "body present with empty payload"; `HasPayload` reflects the difference. Handlers should not rely on `len(Payload) == 0` meaning "no payload".
- **Very large replies.** Payloads are handed to the existing writer path, which fragments messages larger than the negotiated batch size. No new fragmentation logic is required here.

## Migration

- **Public API is additive.** `DeclareQueryable`, `Queryable`, `Query`, `ReplyOption`, and `WithReplyEncoding` are new symbols. Existing symbols, including `Session.Get` and `Subscriber`, are unchanged.
- **Wire behaviour change.** The `NidRequest` branch in `DecodeNetworkStream` changes from "drain and stop" to "decode and continue". Callers observing `DecodedNetwork` values inside an existing test may see additional entries they did not previously see; all existing production callers of `DecodeNetworkStream` are inside this repo and are adjusted in the same change.
- **Version.** Cut `v0.2.0`. The addition of a queryable API is the headline feature; the batched-frame correctness fix is a sub-feature called out in the release notes.
- **Downstream users of PRD-001.** None of the v0.1.0 API signatures change. Upgrading from v0.1.0 to v0.2.0 requires no source modifications.

## Testing

**Unit tests (`go test ./internal/wire/...`).**

- Round-trip `DecodeRequest` against a byte vector produced by hand from the wire spec: `REQUEST` with string key expression, an empty `QUERY` body; with parameters only; with parameters + payload + encoding; with FlagZ extensions present.
- Round-trip `DecodeQueryBody` directly (reader-level) for: `{Q=0}`, `{Q=1, params}`, `{Q=1, params, encoding+payload}`, `{Q=0, encoding+payload}`.
- Golden bytes for `ResponseMsg.Encode` and `ResponseFinalMsg.Encode` (captured by instrumenting a replying `eclipse-zenoh` process or by deriving from the spec).
- Encode/decode round trip for `DeclQueryableBody` and `UndeclQueryableBody`, including inside a `DeclareMsg` batched with a subscriber body.
- **Batched-frame regression test.** Construct a byte slice that contains `REQUEST { QUERY }` followed by `PUSH { PUT }`; assert `DecodeNetworkStream` returns two entries. This test would currently fail and is the explicit proof that the drain-and-stop bug is fixed.

**Unit tests (`go test ./...` at module root).**

- `DeclareQueryable` happy path with a mock transport: assert the `DECLARE` wire message is enqueued with `DeclQueryable` body and the expected `QueryableID`.
- `Queryable.Close` enqueues `UNDECLARE` with the same `QueryableID`.
- Injecting a `REQUEST { QUERY }` frame into the mock transport dispatches to the registered handler with the expected `KeyExpr`, `Parameters`, and `Payload`.
- `Query.Reply` emits a `RESPONSE { REPLY }` with the expected `RequestID` and payload; `Query.Close` emits a single `RESPONSE_FINAL`; calling `Close` twice is idempotent.
- Handler that never calls `Close` still produces exactly one `RESPONSE_FINAL` via the dispatcher's `defer`.
- `handleRequest` with no matching queryable emits `RESPONSE_FINAL` for the offending `RequestID`.
- `goleak.VerifyTestMain` across the package to confirm `DeclareQueryable` / `Close` leave no stray goroutines.

**Python e2e tests (`tests/python-e2e/`).**

- `test_queryable.py`: start a `zenoh-nano-go` queryable on `demo/example/queryable`, run `eclipse-zenoh` Python `session.get("demo/example/queryable")`, assert one payload is returned and the iterator terminates cleanly.
- `test_queryable_wildcard.py`: queryable registered on `demo/**`, query `demo/a`, `demo/a/b`, and `demo/other/c`; all three must dispatch to the same handler and all three `get()` calls complete.
- `test_queryable_no_match.py`: queryable on `demo/foo`, Python client calls `get("demo/bar")`; the call must terminate (with zero replies) rather than blocking to the client timeout.
- `test_queryable_many_replies.py`: handler emits 10 replies then closes; assert the Python `get()` receives exactly 10.

**Manual interop.**

- Run `examples/z_queryable` against the dev router (`tcp/172.26.2.155:31747`); use the `eclipse-zenoh` Python REPL to issue `get` calls and confirm replies.

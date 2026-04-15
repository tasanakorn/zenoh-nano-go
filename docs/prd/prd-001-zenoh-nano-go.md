# PRD-001: zenoh-nano-go — Pure Go Zenoh Client

| Field   | Value                                                    |
|---------|----------------------------------------------------------|
| Status  | Implemented (v0.1.0)                                     |
| Version | v0.1.0                                                   |
| Author  | Tasanakorn (design) + Claude Code (PRD authoring)        |
| Package | github.com/tasanakorn/zenoh-nano-go                      |

## Goals

- Implement the Zenoh wire protocol (version `0x09`) in **pure Go** with **no CGo** and **no C dependencies**, suitable for embedded gateways, sidecars, and constrained edge runtimes.
- Support the **client role only** — connect to a Zenoh router (or peer acting as one) over **TCP unicast**, **UDP unicast**, and discover peers via **UDP multicast scouting** (`udp/224.0.0.224:7446`).
- Expose a **small, idiomatic Go API** with explicit resource lifecycle: `Open` / `Close`, `DeclareSubscriber` / `sub.Close()`, `Put`, `Delete`, `Get`. The shape should feel natural to Go developers while being immediately recognizable to users of `zenoh-pico` (C) and `zenoh-rs` (Rust).
- Match `zenoh-pico v1.8.0` as the **reference for scope and protocol correctness** — every byte on the wire should interoperate with an unmodified `zenohd` router and a `zenoh-pico` peer.
- Ship with a documented codec, unit tests per message type, and at least one end-to-end integration test against a live `zenohd`.

## Non-goals

- **Peer / router / broker roles.** Mesh routing, Gossip scouting, and session-to-session forwarding are explicitly out of scope for v0.1.0.
- **CGo / C dependencies.** No linking against `zenoh-pico`, `zenoh-c`, or any native library. Cross-compilation must work with `CGO_ENABLED=0`.
- **Full feature parity with `eclipse-zenoh/zenoh-go`.** That library wraps the Rust implementation and exposes the full feature matrix; we target a deliberate subset.
- **TinyGo support** in v0.1.0. The code should not gratuitously block TinyGo (prefer `encoding/binary` over `unsafe`, avoid reflection-heavy APIs), but TinyGo is a future milestone, not a release requirement.
- **Liveliness tokens, admin space (`@/…`), raw-Ethernet / serial / BLE transports, shared memory transport, access control / authentication extensions.** Deferred.

## Background & Motivation

Zenoh is a unified pub/sub, query, and storage protocol for distributed systems spanning cloud, edge, and microcontrollers. It has three first-class implementations: the Rust reference (`zenoh`), a C implementation for constrained devices (`zenoh-pico`), and auto-generated bindings (`zenoh-c`, `zenoh-go`, `zenoh-cpp`) that wrap Rust via FFI.

For Go deployments this creates friction:

- The official `eclipse-zenoh/zenoh-go` requires CGo plus a platform-specific Rust shared library. This breaks static cross-compilation, complicates container images (especially `scratch` / distroless), and blocks adoption on platforms where CGo is disabled by policy.
- There is no pure-Go Zenoh client today. Go services that want to talk to a Zenoh fabric have to either take the CGo dependency or reimplement ad-hoc protocol glue.
- `zenoh-pico` proves that a minimal, client-only Zenoh implementation fits in a few thousand lines. Porting its scope to Go yields a small, auditable library that covers the 90% case (publish / subscribe / query against a router) without the CGo tax.

### Current state

- The repository `github.com/tasanakorn/zenoh-nano-go` is empty. There is no existing code, no `go.mod`, no tests, and no prior PRDs.
- Reference artifacts consulted during research:
  - `zenoh-pico` v1.8.0 (protocol constants, message IDs, handshake sequence, wire format).
  - Zenoh wire protocol spec draft (protocol version `0x09`).
  - `zenohd` (the Rust router) as the interop counterparty.
- Nothing in the Go ecosystem needs to be migrated or replaced; this is a greenfield library.

## Design

The library is organized as **four layers**, each with a stable internal seam so components can be tested in isolation and swapped (e.g., a mock transport for codec tests, a fake session for API tests).

```
+--------------------------------------------------------------+
|  API Layer        zenoh.Session, Subscriber, Publisher, ...  |  public
+--------------------------------------------------------------+
|  Session Layer    handshake, keepalive, frame router,        |  internal
|                   declaration registry, reply correlator     |
+--------------------------------------------------------------+
|  Transport Layer  TCP stream, UDP unicast, UDP multicast     |  internal
|                   framing, fragmentation, backpressure       |
+--------------------------------------------------------------+
|  Protocol Layer   codec: varint, wireexpr, messages,         |  internal
|                   extensions, ZenohID, WhatAmI               |
+--------------------------------------------------------------+
```

Proposed Go module layout:

```
github.com/tasanakorn/zenoh-nano-go
├── zenoh.go                  // public API entrypoints (Open, Config)
├── session.go                // Session public type
├── subscriber.go             // Subscriber, Sample
├── publisher.go              // optional Publisher helper
├── query.go                  // Get, Reply
├── errors.go                 // typed errors
├── internal/
│   ├── codec/                // varint, wireexpr, extensions, primitives
│   ├── proto/                // message structs + encode/decode per message
│   ├── transport/            // tcp.go, udp.go, multicast.go, iface.go
│   ├── session/              // handshake, lease/keepalive, frame loop
│   └── scout/                // scouting client
└── examples/
    ├── pub/
    ├── sub/
    └── get/
```

### Protocol Layer

This is the pure codec. Zero I/O, zero goroutines. Everything here is a function from `[]byte` to a typed message or vice versa. It is the most heavily tested layer.

**Primitives (`internal/codec`):**

- `Varint` (a.k.a. `zint`): LEB128, 7 bits per byte, MSB=1 means continuation. Used for lengths, sequence numbers, and integer fields. Implementations:
  `func ReadVarint(b []byte) (uint64, int, error)` and `func AppendVarint(dst []byte, v uint64) []byte`.
- Fixed-width: `uint8` (1 byte), `uint16` little-endian (2 bytes — used for `batch_size` and the TCP length prefix).
- Byte arrays / strings: varint length followed by raw bytes. **No null terminator.**
- `ZenohID`: up to 16 bytes; length is carried in the high nibble of the preceding field byte (same convention as `zenoh-pico`).
- `Encoding`: varint `id<<1 | has_schema`; if `has_schema` is set, a length-prefixed UTF-8 schema string follows.
- **Extensions (Z flag set):** each extension is a 1-byte header (ID + encoding type + "more" flag) followed by a body of type `Unit` (empty), `ZInt` (varint), or `ZBuf` (length-prefixed bytes). Unknown non-mandatory extensions **must be skipped without error**.
- **Message header byte:** low 5 bits (`0x1F`) is the message ID; high 3 bits (`0xE0`) are flags, including the Z flag plus message-specific flags (e.g., `A` = Ack, `S` = Syn echo, `N` = Named key expression).

**Wire-level enumerations:**

- `WhatAmI` on the wire: `Router=0x00`, `Peer=0x01`, `Client=0x02`. Internal bitmask (for Scout target): `Router=0x01`, `Peer=0x02`, `Client=0x04`. The two encodings **must not be confused** — tests will assert both.
- Transport message IDs (`_Z_MID_T_*`): `INIT=0x01`, `OPEN=0x02`, `CLOSE=0x03`, `KEEP_ALIVE=0x04`, `FRAME=0x05`, `FRAGMENT=0x06`, `JOIN=0x07`.
- Scouting: `SCOUT=0x01`, `HELLO=0x02`.
- Network (inside a Frame): `DECLARE=0x1e`, `PUSH=0x1d`, `REQUEST=0x1c`, `RESPONSE=0x1b`, `RESPONSE_FINAL=0x1a`, `INTEREST=0x19`.
- Zenoh body IDs (inside Push/Request/Response): `PUT=0x01`, `DEL=0x02`, `QUERY=0x03`, `REPLY=0x04`, `ERR=0x05`.
- Declarations: `DECL_KEXPR`, `UNDECL_KEXPR`, `DECL_SUBSCRIBER`, `UNDECL_SUBSCRIBER`, `DECL_QUERYABLE`, `UNDECL_QUERYABLE`, `DECL_FINAL`.

**Key expressions (wireexpr):**

Two forms share a single codec:

- **String form:** `ID=0`, with the `N` flag set; followed by a varint length and a UTF-8 suffix. Used before any `DECL_KEXPR` has been sent.
- **Declared (numeric) form:** a single varint numeric ID (without `N`). Used after both sides have agreed on the ID via `DECL_KEXPR`.

Key expressions must be **canonicalized** (no empty chunks, no trailing `/`, `**` not adjacent to another `**`) before being serialized. Canonicalization is a pure string transform exposed as `keyexpr.Canon(string) (string, error)`.

Proposed type sketches:

```go
// internal/proto
type Header struct {
    ID    uint8  // 5-bit message id
    Flags uint8  // 3-bit header flags, including Z (0x80)
}

type InitSyn struct {
    Version     uint8      // 0x09
    WhatAmI     uint8      // wire: client = 0x02
    ZID         ZenohID    // up to 16 bytes
    BatchSize   uint16     // little-endian on wire
    SeqNumRes   uint8      // default 2
    Extensions  []Extension
}

type InitAck struct {
    Version    uint8
    WhatAmI    uint8       // router = 0x00 typically
    ZID        ZenohID
    BatchSize  uint16
    SeqNumRes  uint8
    Cookie     []byte      // opaque, echoed in OpenSyn
    Extensions []Extension
}

type OpenSyn struct {
    Lease     uint64       // milliseconds, varint
    InitialSN uint64       // varint
    Cookie    []byte       // echoed from InitAck
}

type OpenAck struct {
    Lease     uint64
    InitialSN uint64
}

type Frame struct {
    Reliability Reliability // Best-effort / Reliable
    SeqNum      uint64
    Messages    []NetworkMessage // Declare / Push / Request / Response / ResponseFinal
}

type Fragment struct {
    Reliability Reliability
    SeqNum      uint64
    More        bool
    Payload     []byte
}
```

Each message has `func (m *M) AppendTo(dst []byte) []byte` and `func Decode(b []byte) (*M, int, error)`. This avoids allocating an `io.Writer` / `io.Reader` in the hot path and gives the transport layer a simple append-to-scratch-buffer contract.

### Transport Layer

The transport layer exposes a single interface so the session layer does not care how bytes move:

```go
type Transport interface {
    // Send one encoded transport message (framed according to the transport).
    Send(ctx context.Context, msg []byte) error
    // Receive one encoded transport message (unwrap framing).
    Recv(ctx context.Context) ([]byte, error)
    // Local MTU / batch size hint.
    MaxMsgSize() int
    Close() error
}
```

**TCP stream (`internal/transport/tcp.go`):**

- Framing: **2-byte little-endian length prefix** before each serialized transport message.
- Hard cap per message: **65,535 bytes** (the length field is 16-bit).
- Messages larger than `MaxMsgSize()` are split by the session layer into `Fragment` messages (M bit = "more").
- Read loop: read 2 bytes, allocate or reuse a scratch buffer, read N bytes, hand to session.
- Write loop: take a fully-encoded message, prepend length, `Write` the combined slice in one syscall (avoid Nagle + small writes; set `TCP_NODELAY`).

**UDP unicast (`internal/transport/udp.go`):**

- **No length prefix**: one UDP datagram carries exactly one transport message.
- Default batch: **2048 bytes**; negotiated `batch_size` in Init must respect UDP's practical MTU.
- Loss handling: Zenoh's reliability is per-frame with sequence numbers. For v0.1.0 we expose UDP only as "best effort"; we do not implement retransmit.

**UDP multicast scouting (`internal/transport/multicast.go`, `internal/scout`):**

- Default group: `udp/224.0.0.224:7446`.
- Procedure:
  1. Build a `Scout` message with protocol version `0x09`, the target `WhatAmI` bitmask (default `Router|Peer = 0x03`), and a freshly generated `ZenohID`.
  2. Send the datagram to `224.0.0.224:7446`.
  3. Listen for `Hello` datagrams for up to `Config.ScoutTimeout` (default 1000 ms).
  4. Each `Hello` contains the remote `ZenohID`, `WhatAmI`, and a list of locator strings (e.g. `tcp/10.0.0.5:7447`).
  5. Pick the first usable TCP locator, dial it, and drive the Init/Open handshake over the new TCP transport.
- Multicast socket setup needs `net.ListenMulticastUDP` bound to the chosen interface; on Linux we explicitly set `IP_MULTICAST_LOOP` and `IP_MULTICAST_TTL`.

**Fragmentation:**

- Handled at the session layer, below the transport interface, so that TCP (where fragmentation is rarely needed but legal) and UDP (where it is essential above the MTU) share one implementation.
- On send: split the serialized network message across `Fragment` messages of size `MaxMsgSize() - headerOverhead`. All but the last carry the `More` flag.
- On receive: accumulate fragments by `(reliability, sequence-number-prefix)` until a fragment arrives with `More=false`, then decode the reassembled buffer as a `Frame` body.

### Session Layer

The `Session` owns the transport and runs **exactly two goroutines**: a **reader** and a **writer**. All user-facing API calls marshal onto these goroutines via channels. No user goroutine ever touches the transport directly.

**Lifecycle:**

1. `zenoh.Open(Config)` resolves the locator:
   - If `Config.Locator` is set, dial directly.
   - Otherwise, if `Config.Scout` is true, run scouting, then dial the chosen locator.
2. Run the handshake (see below). On success, transition to `open`.
3. Start the reader goroutine (dispatches incoming frames to subscribers, queries, and the keepalive watchdog).
4. Start the writer goroutine (serializes outgoing messages; owns the encode scratch buffer).
5. Start a `time.Ticker` at `lease / 3` that enqueues `KeepAlive` messages if the writer has been idle.

**Handshake (unicast TCP / UDP, client role):**

```
Client                               Router
  |---- InitSyn  (INIT, A=0) ------->|   whatami=0x02, ZID, batch_size=65535,
  |                                  |   seq_num_res=2, version=0x09
  |<--- InitAck  (INIT, A=1) --------|   router ZID, negotiated batch_size,
  |                                  |   negotiated seq_num_res, cookie
  |---- OpenSyn  (OPEN, A=0) ------->|   lease=10000 ms, initial_sn=rand,
  |                                  |   cookie (echoed verbatim)
  |<--- OpenAck  (OPEN, A=1) --------|   router lease, router initial_sn
```

Negotiation rules:

- `seq_num_res`: client proposes `2` (32-bit sequence numbers). Router **may lower** (e.g., to `1` = 16-bit). Client must accept the lower value and use it from that point on.
- `batch_size`: take `min(client, router)`.
- `lease`: each side uses the peer's advertised lease to size its expiry timer. Expire factor = **3** (peer is considered dead after `3 * lease` with no traffic).
- `cookie`: opaque bytes from `InitAck`. Must be echoed **byte-for-byte** in `OpenSyn`. Corrupting or omitting it will cause the router to close the session.

**Steady state (reader goroutine):**

```go
for {
    raw, err := transport.Recv(ctx)
    if err != nil { return closeWith(err) }
    msg, err := proto.DecodeTransport(raw)
    switch m := msg.(type) {
    case *proto.KeepAlive:
        resetLeaseWatchdog()
    case *proto.Frame:
        for _, nm := range m.Messages {
            dispatch(nm) // to subscriber / query correlator / declare registry
        }
    case *proto.Fragment:
        if done, buf := reassembler.Push(m); done {
            dispatch(proto.DecodeFrame(buf))
        }
    case *proto.Close:
        return gracefulClose(m.Reason)
    }
}
```

**Steady state (writer goroutine):**

- Select over: outbound user message channel, keepalive ticker, shutdown channel.
- Serializes each message into a reusable scratch buffer, calls `transport.Send`.
- Maintains the outgoing sequence number (monotonic per reliability class).

**Declaration registry:**

- Map `subscriberID -> callback`, `queryableID -> handler` (reserved for future), `keyExprID -> canonical string`.
- On `DeclareSubscriber`: allocate a local ID, send `DECLARE { DECL_SUBSCRIBER }` with the key expression, register the callback. Incoming `PUSH` messages with a matching key expression are dispatched to the callback from a **per-subscriber goroutine** (one extra goroutine per live subscriber) so a slow callback doesn't block the reader.
- On `sub.Close()`: send `DECLARE { UNDECL_SUBSCRIBER }`, wait for the per-subscriber goroutine to drain, remove from the registry.

**Reply correlator (for `Get`):**

- `Get` allocates a request ID, sends `REQUEST { QUERY }`, and registers a channel in a `map[requestID]chan Reply`.
- Incoming `RESPONSE { REPLY | ERR }` messages are routed to the channel.
- `RESPONSE_FINAL` closes the channel and removes the entry.
- A timeout (from `GetOptions.Timeout`) cancels the context and triggers cleanup.

### API Layer

Public, stable surface. Every exported function is safe for concurrent use unless documented otherwise.

```go
package zenoh

// --- Configuration ------------------------------------------------------

type Config struct {
    // Locator to dial (e.g. "tcp/127.0.0.1:7447"). If empty, Scout must be true.
    Locator string

    // Enable UDP multicast scouting on 224.0.0.224:7446.
    Scout bool

    // Scout timeout (default 1s).
    ScoutTimeout time.Duration

    // Client ZenohID. Random if zero value.
    ZID ZenohID

    // Lease advertised to the peer (default 10s).
    Lease time.Duration

    // Optional logger. nil disables logging.
    Logger Logger
}

// --- Session ------------------------------------------------------------

// Open establishes a session. The returned Session owns background goroutines
// that are stopped by Close.
func Open(ctx context.Context, cfg Config) (*Session, error)

type Session struct { /* unexported */ }

func (s *Session) Put(keyExpr string, payload []byte, opts ...PutOption) error
func (s *Session) Delete(keyExpr string, opts ...DeleteOption) error
func (s *Session) DeclareSubscriber(keyExpr string, cb func(Sample), opts ...SubOption) (*Subscriber, error)
func (s *Session) Get(ctx context.Context, selector string, opts ...GetOption) (<-chan Reply, error)
func (s *Session) Close() error

// --- Subscriber ---------------------------------------------------------

type Subscriber struct { /* unexported */ }
func (s *Subscriber) Close() error

type Sample struct {
    KeyExpr   string
    Payload   []byte
    Kind      SampleKind // Put | Delete
    Encoding  Encoding
    Timestamp *time.Time // optional
}

// --- Get ----------------------------------------------------------------

type Reply struct {
    Sample Sample
    Err    error    // non-nil for ERR replies
    Source ZenohID  // replying entity
}
```

Design principles the API enforces:

- **No hidden goroutines started by the user.** `Open` starts the session's internal reader/writer; `DeclareSubscriber` starts the per-subscriber dispatch goroutine. Everything is stopped by `Close`.
- **Context for cancellation.** `Open`, `Get`, and any future blocking operation accept `context.Context`. `Put` is non-blocking (enqueues to the writer goroutine) so it takes no context.
- **Errors are returned, never panicked.** Protocol violations surface as typed errors in `errors.go` (`ErrHandshake`, `ErrClosed`, `ErrProtocol`, `ErrTimeout`).
- **Subscriber callbacks are called sequentially** per subscriber. Users who want parallelism spawn their own goroutines inside the callback.
- **Session is safe for concurrent use**: `Put`, `Delete`, `DeclareSubscriber`, and `Get` can all be called from multiple goroutines simultaneously.

## Changes by Component

| Component                         | Path                                              | Change                                                                                                                                                           |
|-----------------------------------|---------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Module bootstrap                  | `go.mod`, `go.sum`                                | Create module `github.com/tasanakorn/zenoh-nano-go`, Go 1.22+, no external deps beyond stdlib.                                                                   |
| Codec primitives                  | `internal/codec/`                                 | New. Varint, uint16 LE, length-prefixed bytes, ZenohID, Encoding, Extensions, header-byte helpers.                                                               |
| Key expression canonicalizer      | `internal/codec/keyexpr.go`                       | New. `Canon(string) (string, error)` plus wireexpr (string + declared) codec.                                                                                    |
| Transport messages                | `internal/proto/transport.go`                     | New. Encode/decode for INIT, OPEN, CLOSE, KEEP_ALIVE, FRAME, FRAGMENT, JOIN.                                                                                     |
| Scouting messages                 | `internal/proto/scout.go`                         | New. Encode/decode for SCOUT and HELLO.                                                                                                                          |
| Network messages                  | `internal/proto/network.go`                       | New. DECLARE, PUSH, REQUEST, RESPONSE, RESPONSE_FINAL, INTEREST.                                                                                                 |
| Zenoh bodies                      | `internal/proto/body.go`                          | New. PUT, DEL, QUERY, REPLY, ERR.                                                                                                                                |
| Declarations                      | `internal/proto/declare.go`                       | New. DECL_KEXPR, UNDECL_KEXPR, DECL_SUBSCRIBER, UNDECL_SUBSCRIBER, DECL_QUERYABLE, UNDECL_QUERYABLE, DECL_FINAL (the last four encode/decode only; runtime may ignore). |
| Transport interface               | `internal/transport/transport.go`                 | New. `Transport` interface + factory.                                                                                                                            |
| TCP transport                     | `internal/transport/tcp.go`                       | New. 2-byte LE length prefix, `TCP_NODELAY`, bounded read/write buffers.                                                                                         |
| UDP unicast transport             | `internal/transport/udp.go`                       | New. Datagram = message, batch size 2048.                                                                                                                        |
| UDP multicast transport           | `internal/transport/multicast.go`                 | New. Bound listener on `224.0.0.224:7446`, TTL/loopback controls.                                                                                                |
| Scouting client                   | `internal/scout/scout.go`                         | New. `Scout(ctx, target WhatAmI, timeout) ([]Hello, error)`.                                                                                                     |
| Session core                      | `internal/session/session.go`                     | New. Handshake, keepalive, reader/writer goroutines, declaration registry, reply correlator, fragmenter / reassembler.                                           |
| Public API                        | `zenoh.go`, `session.go`, `subscriber.go`, `query.go`, `publisher.go`, `errors.go` | New. Matches the API Layer sketch above.                                                                                                                         |
| Examples                          | `examples/pub`, `examples/sub`, `examples/get`    | New. Three small binaries used for manual interop against `zenohd`.                                                                                              |
| Unit tests                        | `internal/codec/*_test.go`, `internal/proto/*_test.go` | New. Table-driven encode/decode round trips, golden-byte vectors captured from `zenoh-pico`.                                                                     |
| Integration tests                 | `integration/`                                    | New. Spins up `zenohd` via `docker run` (skipped if docker unavailable) and exercises Put/Sub/Get.                                                               |
| Docs                              | `docs/prd/prd-001-zenoh-nano-go.md`, `docs/README.md` | New. This PRD and the PRD index.                                                                                                                                 |
| CI                                | `.github/workflows/ci.yml`                        | New (follow-up). `go test ./...` with `CGO_ENABLED=0`, `go vet`, `staticcheck`, race detector.                                                                   |

## Edge Cases

- **Router lowers `seq_num_res`.** Client must accept and switch to narrower sequence numbers (e.g., 16-bit instead of 32-bit). All sequence-number arithmetic must be wrapping with the configured modulus.
- **Cookie mismatch / drop.** If `OpenSyn` is sent without echoing the exact bytes from `InitAck`, the router will close. Store the cookie as an opaque `[]byte` and never rewrite it.
- **Lease expiry.** If no data has been received within `3 * lease`, declare the session dead, surface `ErrTimeout` to in-flight operations, close the transport.
- **KeepAlive pacing.** Send a `KeepAlive` whenever the writer has been idle for `lease / 3`. Any other outbound message resets the timer.
- **Fragmented payloads larger than the TCP max (65535).** On TCP, the 2-byte length prefix caps a single serialized transport message at 65535 bytes. Larger payloads must be carried in `Fragment` chains; on the receive side, the reassembled buffer may exceed 65535 bytes — the reassembler must allocate dynamically.
- **Unknown non-mandatory extensions.** Must be skipped without error so a newer router does not break an older client. Mandatory extensions (Z flag + "mandatory" bit) must cause the message to be rejected if unknown.
- **Invalid UTF-8 in key expressions.** Reject at canonicalization time with `ErrInvalidKeyExpr`; never send to the wire.
- **Scouting returns no Hello.** `Open` returns `ErrScoutTimeout` after `Config.ScoutTimeout`. The caller can retry or fall back to a configured locator.
- **Multicast interface selection.** On hosts with multiple interfaces, `net.ListenMulticastUDP(nil, ...)` can be unreliable. Honour `Config.MulticastInterface` when set; otherwise iterate interfaces and pick the first non-loopback with multicast support.
- **Concurrent `Put` from many goroutines.** The writer goroutine is the single serializer; the outbound channel has a bounded buffer. If full, `Put` blocks (or, with `PutOptions.NonBlocking`, returns `ErrBackpressure`).
- **`Close` during an in-flight `Get`.** Pending reply channels are closed; readers receive a final zero-value `Reply` with `Err = ErrClosed`.
- **CLOSE from peer mid-stream.** Drain the reader, mark the session closed, propagate reason code to all waiting operations.
- **Endianness.** `batch_size` and the TCP length prefix are little-endian **regardless of host**. Varints are byte-wise and therefore endianness-neutral; tests must run on both little- and big-endian if possible (qemu-mips64).
- **UDP datagram loss.** Expected on lossy networks. For v0.1.0, UDP is best-effort; retransmit is deferred.

## Migration

- **No prior Go code exists**, so there is no API migration to perform.
- **For users coming from `eclipse-zenoh/zenoh-go`:** API names align where possible (`Open`, `DeclareSubscriber`, `Put`, `Get`), but the shape is smaller. A short "differences from `zenoh-go`" section will live in `docs/` alongside v0.1.0.
- **For users coming from `zenoh-pico`:** protocol behaviour is intentionally identical; this library can be used as a drop-in client for the same routers. The API shape is Go-idiomatic (channels for `Get`, callback for `Subscribe`) rather than C-idiomatic.
- **Backwards compatibility policy going forward:** once v0.1.0 ships, the public API in the root package is covered by Go's standard module semver rules. Anything under `internal/` may change without notice.

## Testing

**Unit tests (`go test ./internal/...`):**

- **Codec round trips.** For every message type, assert `Decode(Encode(m)) == m` on a table of hand-crafted cases covering: empty payloads, maximum-length payloads, all flag combinations (Z / A / S / N / M bits), multiple extensions, unknown-extension skipping.
- **Golden byte vectors.** Capture reference bytes by instrumenting `zenoh-pico` (or `zenohd` with `RUST_LOG=trace`) during a known interaction and check our encoder produces the same bytes. At minimum: InitSyn, OpenSyn, a simple Put, a simple Subscribe declare, a KeepAlive.
- **Varint boundary tests.** `0`, `1`, `127`, `128`, `16383`, `16384`, `2^32-1`, `2^64-1`; malformed inputs (10+ continuation bytes) must return `ErrVarintOverflow`.
- **Key expression canonicalization.** Table of `(input, canonical, shouldError)` covering wildcards, empty chunks, trailing slashes, invalid UTF-8.
- **Fragment reassembly.** Randomized test: serialize a large `Frame`, chop it into random-sized fragments, feed them into the reassembler, assert the original `Frame` is reconstructed.
- **Fuzz tests** (`go test -fuzz`). Fuzz the decoder for every message type; invariant: decoding must not panic and must either succeed or return a typed error.

**Integration tests (`go test ./integration -tags=integration`):**

- Spin up `zenohd` via `docker run --network host eclipse/zenoh:latest` (skip the whole package if docker is unavailable or `ZENOH_NANO_SKIP_INTEGRATION=1`).
- **Pub/Sub echo:** subscribe on `"test/**"`, publish 100 messages to `"test/a"`, assert the subscriber sees all 100 in order.
- **Get against a storage:** load `zenoh-plugin-storage-manager` with an in-memory backend, `Put` a value, `Get` it back, assert payload equality.
- **Handshake robustness:** connect, disconnect, reconnect 100 times; assert no goroutine leaks (`goleak`).
- **Keepalive:** open a session, do nothing for `4 * lease`, assert the session is still alive (because KeepAlives were sent) and a subsequent `Put` still succeeds.
- **Graceful close:** `Close()` in the middle of a subscription; assert the router observes a `CLOSE` message and the session's goroutines exit within 1 second.

**Interoperability tests (`go test ./interop -tags=interop`):**

- Start a `zenoh-pico` example publisher; assert `zenoh-nano-go` subscriber receives its payloads byte-for-byte.
- Start a `zenoh-nano-go` publisher; assert a `zenoh-pico` example subscriber receives the same payloads.
- Repeat the two above across TCP and UDP unicast.

**Race & leak detection:**

- All test targets run with `-race`.
- `goleak.VerifyTestMain` in `internal/session` and `integration` to catch stray goroutines.

**Benchmarks (nice-to-have for v0.1.0):**

- `BenchmarkEncodePushSmall` / `BenchmarkDecodePushSmall` at 64-byte payloads.
- `BenchmarkEndToEndPutSub` through a local TCP loopback session, reporting messages/sec and allocations/op. Target: zero allocations per `Put` on the steady-state hot path (scratch buffer reuse).

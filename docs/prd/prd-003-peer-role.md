# PRD-003: Peer Role

| Field   | Value                                             |
|---------|---------------------------------------------------|
| Status  | Proposed                                          |
| Version | v0.3.0                                            |
| Author  | Tasanakorn (design) + Claude Code (PRD authoring) |
| Package | github.com/tasanakorn/zenoh-nano-go               |

## Goals

- Add a `Role` field to `Config` so callers can opt in to **peer mode** (default stays `Client`, preserving PRD-001 behaviour byte-for-byte). The only wire-visible effect in v0.3.0 is the `WhatAmI` byte sent during INIT/HELLO; every other code path behaves identically.
- Implement a **TCP listener** so a peer can accept incoming TCP connections and run the **server-side Init/Open handshake** (receive `InitSyn` → send `InitAck` → receive `OpenSyn` → send `OpenAck`). The accepted side reuses the existing `Session` reader/writer machinery verbatim once the handshake completes.
- Implement **`JOIN` message encode/decode** and **multicast-based peer discovery**: a peer announces itself on the multicast group via periodic `JOIN` frames, listens for `JOIN` frames from other peers on the same group, and dials peers it discovers. Direct `Config.Connect` dialing also remains available for deterministic test setups.
- Extend the scouting side with **`HelloMsg.Encode`** so a peer can respond to `Scout` requests with its own locators; this is the inbound complement to the `DecodeHello` shipped in v0.1.0.
- Interoperate with `zenohd` (peer role) and with `zenoh-pico` peers: two `zenoh-nano-go` peers must be able to discover each other via multicast, establish a bidirectional TCP session, and exchange Pub/Sub/Query traffic using the exact same public API as v0.2.0.

## Non-goals

- **Router / broker role.** Routing tables, gossip-based scouting convergence, session-to-session message forwarding, and admin-space handling remain out of scope. A peer accepts inbound sessions but does **not** forward messages between them.
- **Mesh / multi-hop routing.** Full peer-to-peer mesh (every peer reachable via every other peer) is deferred to a future PRD. In v0.3.0 a peer is reachable only by entities that share a direct TCP session with it.
- **TLS peer sessions.** TCP is the only transport in v0.3.0; TLS/QUIC remains deferred as documented in `docs/gap-vs-zenoh-pico.md`.
- **Peer-to-peer UDP data transport.** UDP unicast stays client-only for data; UDP multicast is used only for `JOIN`/`SCOUT` discovery announcements in peer mode.
- **Admin space, liveliness tokens, access control.** Unchanged from PRD-001 non-goals.
- **`WhatAmI = Peer` affecting routing decisions.** The byte is negotiated honestly but no code path branches on it beyond handshake accounting; queryable routing, subscriber fanout, and reply correlation continue to be session-local.
- **Graceful peer churn handling beyond session death.** If a peer disconnects mid-stream, the existing lease/watchdog flow in `session.go` tears the session down. Automatic reconnection, connection pooling, and reconnect backoff are deferred.
- **IPv6 multicast.** IPv4 multicast is the only announcement channel in v0.3.0, matching PRD-001.

## Background & Motivation

PRD-001 shipped `zenoh-nano-go` as a **client-only** library: every session dials *out* to a router-like entity (typically `zenohd`), proposes `WhatAmI=Client`, and drives the client half of the Init/Open handshake. PRD-002 added the queryable API but kept the same client-role constraint.

The real-world deployment pattern for edge gateways increasingly wants **peer-to-peer** operation: two Go processes on the same LAN should be able to exchange Pub/Sub traffic without standing up a router. `zenoh-pico` v1.8.0 supports this via `Z_FEATURE_MULTI_THREAD + Z_FEATURE_LINK_TCP + Z_FEATURE_SCOUTING_MULTICAST`, where a peer both dials discovered peers and accepts inbound TCP connections. Porting this capability unlocks router-free deployments and closes the largest remaining role gap versus `zenoh-pico` (see `docs/gap-vs-zenoh-pico.md`, which currently marks "Peer role (mesh)" as `No - wont`).

The protocol primitives needed are narrow: a `JOIN` transport message, a server-side handshake, a TCP listener, an outbound `Hello`, and one new config knob. The *session machinery itself* — reader/writer goroutines, declaration registry, subscriber dispatch, reply correlator, fragment reassembler, keepalive/watchdog — is already role-agnostic in v0.2.0 and needs no changes.

### Current state

- **`config.go:6-17`** has `Config` with `Connect`, `Lease`, `HandshakeTimeout`, `ZID`, `WriteQueueSize`. No `Role` field, no `Listen` field.
- **`session.go:129-135`** hardcodes `WhatAmI: wire.WhatAmIClientIdx` in the outbound `InitSyn`. This is the single site where the role byte is chosen.
- **`internal/wire/transport_msg.go:286-318`** `DecodeTransport` errors on `MidInit` without the `A` flag ("unexpected InitSyn from peer") and on `MidOpen` without the `A` flag ("unexpected OpenSyn from peer"). `MidJoin` falls through to the `default` case and returns `"unknown transport mid 0x07"`. The `InitSyn`, `OpenSyn` structs exist (used for outbound encode only); no decoders exist for them.
- **`internal/wire/header.go:11`** defines `MidJoin = 0x07`. No `JoinMsg` struct, no encode, no decode.
- **`internal/wire/scouting.go:47-98`** has `DecodeHello` but **no `HelloMsg.Encode`**. A peer cannot currently respond to a `Scout` message.
- **`internal/session/tcp.go:19-33`** `dialTCP` constructs a `tcpTransport` from a dialed `net.Conn`. No accept-side constructor (`newTCPTransportFromConn`) exists.
- **`internal/session/mux.go`** is a 3-line placeholder comment file reserved for future multiplexing logic; it is the natural home for accept-loop demux.
- **`internal/session/transport.go:32-45`** has `DialTransport`; no `ListenTransport` counterpart.
- **`scout.go:24-28`** defines the public `WhatAmI` enum (`WhatAmIRouter=0, WhatAmIPeer=1, WhatAmIClient=2`). The same indices appear as `wire.WhatAmI*Idx` in `internal/wire/header.go:73-78`, and as bitmasks `wire.WhatAmI*Bit` in `internal/wire/header.go:82-85`. **No helper function** converts between the two encodings; PRD-001 notes this is a known hazard.
- **`docs/gap-vs-zenoh-pico.md:37`** marks "Peer role (mesh)" as `No - wont`. This PRD promotes it to `Yes` in v0.3.0 (narrowly — see Non-goals re: full mesh).

## Design

The implementation is additive. Every existing client-mode code path is left alone; peer mode adds (a) a different `WhatAmI` byte, (b) an accept path that mirrors the existing dial path, and (c) an optional `JOIN` announcer.

```
+-------------------------------------------------------------------+
|  Public API                                                       |
|    Config.Role = Peer, Config.Listen = []string                   |
|    zenoh.Open(cfg) still returns *Session                         |
+-------------------------------------------------------------------+
|  peer.go                                                          |
|    accept loop ──▶ per-inbound-session Session                    |
|    JOIN announcer (multicast send + recv)                         |
|    join-based dialer ──▶ reuses DialTransport                     |
+-------------------------------------------------------------------+
|  session.go                                                       |
|    Open() reads Config.Role, sets WhatAmI                         |
|    OpenAccepted() runs server-side handshake, then the exact      |
|    same reader/writer machinery as Open()                         |
+-------------------------------------------------------------------+
|  internal/session/                                                |
|    ListenTransport, newTCPTransportFromConn                       |
+-------------------------------------------------------------------+
|  internal/wire/                                                   |
|    JoinMsg.Encode / DecodeJoin                                    |
|    DecodeInitSyn, DecodeOpenSyn (server-side)                     |
|    HelloMsg.Encode                                                |
|    WhatAmIIdxToBit, WhatAmIBitToIdx helpers                       |
+-------------------------------------------------------------------+
```

### Config additions

```go
// Role is the role this session advertises to the remote peer.
type Role uint8

const (
    RoleClient Role = iota // default, preserves v0.2.0 behaviour
    RolePeer
)

type Config struct {
    // existing fields unchanged
    Connect          []string
    Lease            time.Duration
    HandshakeTimeout time.Duration
    ZID              ZenohID
    WriteQueueSize   int

    // Role advertised during INIT. Default: RoleClient.
    Role Role

    // Listen is the ordered list of listen locators, e.g. "tcp/0.0.0.0:7447"
    // or "tcp/0.0.0.0:0" to let the OS pick a port. Only consulted when
    // Role == RolePeer. Empty slice ⇒ no listener (outbound-only peer).
    Listen []string

    // ScoutAddr overrides the multicast group for JOIN announcements. Empty
    // uses DefaultScoutAddr ("224.0.0.224:7446"). Only consulted when
    // Role == RolePeer.
    ScoutAddr string

    // JoinInterval is the period between JOIN announcements. Default: 1s.
    // Only consulted when Role == RolePeer.
    JoinInterval time.Duration
}
```

`DefaultConfig()` is unchanged (Role zero-value is `RoleClient`). A new `DefaultPeerConfig()` helper returns a config pre-populated for peer mode with sensible defaults (`Role=RolePeer`, `Listen=["tcp/0.0.0.0:0"]`, `JoinInterval=1*time.Second`).

### Wire codec additions (`internal/wire/`)

Three new concerns, each with a round-trip unit test.

**1. `JoinMsg` encode + decode (`internal/wire/transport_msg.go`).**

`JOIN` has the same shape as `InitSyn` *without* the A-flag semantic, plus a mandatory `lease` varint on the end (peer announces the lease it intends to honour on any session established from this announcement).

```
header(MidJoin, flags)        flags: Z=extensions, S=non-default resolution/batch, T=lease in seconds
version   : u8
flags_byte: (whatami & 0x03) | ((zid_len-1) << 4)
zid       : zid_len bytes
[resolution, batch_size LE u16]  -- only if S flag
lease_ms  : uvarint  (if T is set, value is in seconds and decoded ×1000)
extensions: only if Z flag
```

```go
type JoinMsg struct {
    WhatAmI    uint8       // wire index: 0=Router, 1=Peer, 2=Client
    ZID        []byte
    Resolution uint8       // defaults to DefaultResolution
    BatchSize  uint16      // defaults to DefaultBatchSize
    LeaseMs    uint64
}

func (m *JoinMsg) Encode() []byte
func decodeJoin(r *bufio.Reader, flags uint8, hasZ bool) (*JoinMsg, error)
```

Encoder sets `FlagS` only when `Resolution != DefaultResolution || BatchSize != DefaultBatchSize` (mirrors `InitSyn.Encode`). Encoder does **not** set `FlagT` (lease always advertised in ms). Decoder honours both `FlagS` and `FlagT` for robustness against other implementations.

Add a `MidJoin` case to `DecodeTransport`:

```go
case MidJoin:
    return decodeJoin(r, flags, hasZ)
```

**2. Server-side handshake decoders (`internal/wire/transport_msg.go`).**

`InitSyn` and `OpenSyn` currently have only encoders. Add decoders mirroring `decodeInitAck` and `decodeOpenAck` byte-for-byte, then extend `DecodeTransport`:

```go
case MidInit:
    if flags&FlagA != 0 {
        return decodeInitAck(r, flags, hasZ)
    }
    return decodeInitSyn(r, flags, hasZ)
case MidOpen:
    if flags&FlagA != 0 {
        return decodeOpenAck(r, flags, hasZ)
    }
    return decodeOpenSyn(r, flags, hasZ)
```

`decodeInitSyn` reads: version byte, flags byte (→ WhatAmI + ZID length), ZID bytes, optional resolution + batchSize (iff `FlagS`), extensions. Returns `*InitSyn`.

`decodeOpenSyn` reads: lease varint (×1000 if `FlagT`), initialSN varint, cookie, extensions. Returns `*OpenSyn`. Note the cookie here is the one *the server* issued in its preceding `InitAck` and is echoed back by the client — the server-side handshake driver in `peer.go` must validate it byte-for-byte (see "Server-side handshake" below).

**3. `HelloMsg.Encode` (`internal/wire/scouting.go`).**

Inverse of `DecodeHello`:

```go
func (m *HelloMsg) Encode() []byte {
    var buf []byte
    var flags uint8
    if len(m.Locators) > 0 {
        flags |= FlagL
    }
    buf = append(buf, MakeHeader(MidHello, flags))
    buf = append(buf, m.Version)
    whatByte := (m.WhatAmI & 0x03) | (uint8(len(m.ZID)-1) << 4)
    buf = append(buf, whatByte)
    buf = append(buf, m.ZID...)
    if len(m.Locators) > 0 {
        buf = AppendUvarint(buf, uint64(len(m.Locators)))
        for _, loc := range m.Locators {
            buf = AppendZString(buf, loc)
        }
    }
    return buf
}
```

**4. WhatAmI conversion helpers (`internal/wire/header.go`).**

```go
// WhatAmIIdxToBit converts the 2-bit INIT/HELLO index form into the Scout bitmask form.
func WhatAmIIdxToBit(idx uint8) uint8 {
    switch idx {
    case WhatAmIRouterIdx: return WhatAmIRouterBit
    case WhatAmIPeerIdx:   return WhatAmIPeerBit
    case WhatAmIClientIdx: return WhatAmIClientBit
    }
    return 0
}

// WhatAmIBitToIdx converts a single-bit Scout form to the INIT/HELLO index form.
// Returns (idx, ok). ok is false if `bit` has zero or multiple bits set.
func WhatAmIBitToIdx(bit uint8) (uint8, bool)
```

These helpers exist to keep the "two WhatAmI encodings" hazard (documented in `CLAUDE.md` and `docs/wire-format-notes.md`) behind a tested seam instead of ad-hoc switch statements scattered through `peer.go` and `scout.go`.

### Transport listener (`internal/session/`)

**`internal/session/tcp.go`** gains an accept-side constructor and a listener factory:

```go
// newTCPTransportFromConn wraps an already-accepted net.Conn as a Transport.
// It mirrors dialTCP's framing and buffering.
func newTCPTransportFromConn(conn net.Conn) Transport {
    if tcpConn, ok := conn.(*net.TCPConn); ok {
        _ = tcpConn.SetNoDelay(true)
    }
    return &tcpTransport{
        conn:   conn,
        r:      bufio.NewReaderSize(conn, 65536),
        remote: "tcp/" + conn.RemoteAddr().String(),
    }
}

// listenTCP starts a net.Listener and returns a Listener that yields
// Transports on Accept.
func listenTCP(addr string) (Listener, error)
```

**`internal/session/transport.go`** adds a parallel `Listener` interface plus a `ListenTransport` factory:

```go
// Listener is a bidirectional transport listener.
type Listener interface {
    // Accept blocks until a new Transport is established or the listener is closed.
    Accept() (Transport, error)
    // Close stops the listener; pending Accepts return an error.
    Close() error
    // LocalLocator returns the locator string clients should dial (e.g. "tcp/0.0.0.0:45821").
    LocalLocator() string
}

// ListenTransport opens a Listener on the given locator.
func ListenTransport(locator string) (Listener, error) {
    scheme, addr, err := ParseLocator(locator)
    if err != nil {
        return nil, err
    }
    switch scheme {
    case "tcp":
        return listenTCP(addr)
    default:
        return nil, fmt.Errorf("unsupported listen scheme %q", scheme)
    }
}
```

**`internal/session/mux.go`** stops being a placeholder. It becomes the accept-loop plumbing used by `peer.go`:

```go
// AcceptLoop drains l.Accept() until ctx is done or the listener is closed,
// handing each accepted Transport to handle. handle is expected not to block
// the loop (it should spawn its own goroutine for the handshake).
func AcceptLoop(ctx context.Context, l Listener, handle func(Transport))
```

### Server-side handshake (`peer.go`)

A new top-level file `peer.go` owns everything role-specific: the accept loop, the server-side handshake driver, the `JOIN` announcer, and the discovery-driven dialer. It does **not** replace `session.go`'s reader/writer machinery; once the handshake completes it hands the `Transport` plus negotiated parameters to the same constructor path `Open` uses today.

**Handshake sequence (server side):**

```
Remote                           This peer
  |                                  |
  |---- InitSyn (INIT, A=0) -------->|   decodeInitSyn → validate version
  |                                  |   generate cookie, record peer ZID + negotiated params
  |<--- InitAck (INIT, A=1) ---------|   WhatAmI=peer idx, our ZID, min(batch), cookie
  |                                  |
  |---- OpenSyn (OPEN, A=0) -------->|   decodeOpenSyn → validate cookie bytewise
  |                                  |
  |<--- OpenAck (OPEN, A=1) ---------|   our lease, initialSN=0
  |                                  |
  |       ---- steady state ----     |   reader/writer loops start here
```

Refactor: extract the steady-state startup (spawning `readerLoop`, `writerLoop`, `keepAliveLoop`, `watchdogLoop`; initializing `s.mu`-guarded maps; returning `*Session`) from the tail of `Open` into a helper `newSessionFromTransport(tr, peerZID, snMask, agreedBatch, peerLeaseMs, ...) *Session`. Both `Open` (client) and the new accept handler call this helper after the handshake. All negotiation parameters (batch size min, resolution mask, SN mask) are computed identically in both paths.

**Cookie:** the server issues a cookie in `InitAck`. `zenoh-pico` uses random bytes; we match: 16 random bytes per accept. The cookie is stored on the per-accept handshake context and compared byte-for-byte against the cookie in the arriving `OpenSyn`. Mismatch ⇒ close the transport with `ErrCatProtocol`.

### Peer API surface (`peer.go`)

```go
// Open detects Role from cfg and picks client or peer startup. Public signature is unchanged.
func Open(ctx context.Context, cfg Config) (*Session, error)
```

In peer mode, `Open` returns a `*Session` representing *this local peer*. Unlike client mode (where a Session is 1:1 with a remote router), a peer Session in v0.3.0 is **still 1:1 with one remote peer** — the Session returned from `Open` represents the *first* peer discovered or connected to. Additional inbound/outbound peers surface as separate `*Session` values via a callback:

```go
// OnPeerConnected is called (on a goroutine) for every Session established while
// this peer is running, including the first one returned by Open. nil disables
// the callback (the caller must poll via accessor).
type PeerHandler func(*Session)

type Config struct {
    // ...existing fields...
    // OnPeerConnected fires once per established session (inbound or outbound).
    // Called after the handshake completes and the Session is usable.
    OnPeerConnected PeerHandler
}
```

Rationale: reusing `*Session` as the per-peer-session handle keeps the public API surface unchanged. Users who care only about "any peer I can publish to" treat the return of `Open` as today. Users who want to fan out to multiple peers provide `OnPeerConnected`. Multi-session publish/subscribe fanout is still out of scope; each callback gets its own `*Session`, and application code decides whether to broadcast.

### JOIN announcer and discovery

`peer.go` owns three goroutines (only when `cfg.Role == RolePeer`):

1. **JOIN sender.** Every `cfg.JoinInterval` (default 1s), send a `JoinMsg` to the multicast group. Contents: our ZID, `WhatAmI=WhatAmIPeerIdx`, `LeaseMs=cfg.Lease`. Uses the existing `golang.org/x/net/ipv4` machinery already imported by `scout.go`.
2. **JOIN receiver.** Join the multicast group, read datagrams, attempt `DecodeTransport` → `*JoinMsg`. For each JOIN from a ZID we have not already connected to, dial the first TCP locator advertised by the sender. In v0.3.0, JOIN does not carry locators itself (the wire format above matches `zenoh-pico`), so discovery alone is insufficient; callers pair JOIN with either a separate `Scout`/`Hello` round or with `Config.Connect` entries that enumerate known peers. This is documented as a v0.3.0 limitation; adding locator extensions to JOIN (`_Z_MSG_EXT_JOIN_LOCATORS`) is a follow-up PRD.
3. **SCOUT responder.** Listen for `ScoutMsg` datagrams on the multicast group; when one arrives, reply with a `HelloMsg` carrying our listener locators (derived from `Listen` entries with `0.0.0.0` rewritten to the interface address observed on the incoming multicast packet).

**Scope compromise for v0.3.0.** Because `JoinMsg` in the current spec has no locator extension, cross-peer discovery in this release works in two stages: `JOIN` signals "peer with this ZID is alive on this subnet"; a follow-up `SCOUT → HELLO` exchange between the discoverer and the announcer resolves the ZID to a dialable TCP locator. If callers want fully JOIN-driven discovery, they pre-populate `Config.Connect` with explicit locators (the deterministic path, suitable for tests and for deployments with known topology). Full JOIN-locator extensions are explicitly deferred.

### `session.go` edits

Four sites change; none break existing client-mode behaviour.

1. **`Open` role branch.** At the top of `Open`, branch on `cfg.Role`. For `RoleClient`, continue exactly as today. For `RolePeer`, delegate to `openPeer(ctx, cfg)` in `peer.go`.
2. **WhatAmI byte.** In the client path (`session.go:131`), compute `whatAmI := wire.WhatAmIClientIdx`. For the unlikely case a user sets `Role=RolePeer` but only fills `Config.Connect` (i.e., peer-as-dialer without listening), this single site is *also* the server's WhatAmI byte in the accept path once `openPeer` calls `newSessionFromTransport`. Consolidating so both paths pass the chosen index through explicitly.
3. **Extract `newSessionFromTransport`.** Move the steady-state setup from `Open` into this helper so both `Open` (client) and `openPeer`'s accept handler can call it after finishing their respective handshakes.
4. **Expose the writer-enqueue seam.** `openPeer` needs to send `HelloMsg.Encode()` and `JoinMsg.Encode()` datagrams on a multicast socket — these are *not* transport-framed (no u16 length prefix), so they don't go through the Session writer. This is handled entirely inside `peer.go` via a dedicated `net.PacketConn`. No changes to `session.go`'s writer.

## Changes by Component

| Component                        | Path                                   | Change                                                                                                                                                          |
|----------------------------------|----------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Config: Role + Listen            | `config.go`                            | Add `Role Role`, `Listen []string`, `ScoutAddr string`, `JoinInterval time.Duration`, `OnPeerConnected PeerHandler` fields. Add `Role` type + constants. Add `DefaultPeerConfig()`. |
| Session: role-aware WhatAmI      | `session.go`                           | Modify `Open` to branch on `cfg.Role`; extract `newSessionFromTransport` helper used by both client and peer paths; pass WhatAmI index explicitly.              |
| Peer: accept + JOIN              | `peer.go`                              | Add. `openPeer(ctx, cfg) (*Session, error)`, accept loop, server-side handshake driver, JOIN sender/receiver, SCOUT responder, discovery-driven dialer.         |
| Wire: JoinMsg                    | `internal/wire/transport_msg.go`       | Add `JoinMsg` struct, `Encode` method, `decodeJoin`; add `MidJoin` case in `DecodeTransport`.                                                                   |
| Wire: server-side handshake      | `internal/wire/transport_msg.go`       | Add `decodeInitSyn`, `decodeOpenSyn`; extend `DecodeTransport`'s `MidInit`/`MidOpen` cases to return `*InitSyn` / `*OpenSyn` when A flag clear.                 |
| Wire: HelloMsg.Encode            | `internal/wire/scouting.go`            | Add `HelloMsg.Encode()` mirroring `DecodeHello`.                                                                                                                |
| Wire: WhatAmI helpers            | `internal/wire/header.go`              | Add `WhatAmIIdxToBit` and `WhatAmIBitToIdx` conversion helpers.                                                                                                 |
| Wire: round-trip tests           | `internal/wire/*_test.go`              | Add tests for `JoinMsg` encode/decode, `decodeInitSyn`, `decodeOpenSyn`, `HelloMsg.Encode`, `WhatAmIIdxToBit`/`WhatAmIBitToIdx`.                                 |
| Session: TCP accept              | `internal/session/tcp.go`              | Add `newTCPTransportFromConn`; add `listenTCP` returning a `Listener`; add `tcpListener` struct implementing `Listener`.                                        |
| Session: Listener interface      | `internal/session/transport.go`        | Add `Listener` interface and `ListenTransport(locator string) (Listener, error)` factory parallel to `DialTransport`.                                           |
| Session: AcceptLoop              | `internal/session/mux.go`              | Replace placeholder with `AcceptLoop(ctx, l, handle)` helper that calls `Accept` in a loop until ctx/listener closes.                                           |
| Peer tests                       | `peer_test.go`                         | Add. Two-peer-over-loopback smoke test: peer A listens, peer B dials via `Config.Connect`, each registers a subscriber/publisher, assert payload delivery.      |
| Example                          | `examples/z_peer/`                     | Add. CLI peer that listens on a configurable port, dials a list of `--connect` locators, subscribes to `--key`, and optionally publishes on a timer.            |
| Python e2e tests                 | `tests/python-e2e/`                    | Add `test_peer_interop.py`: start a `zenoh-nano-go` peer and an `eclipse-zenoh` Python peer (`config.mode = "peer"`) on loopback; assert Pub/Sub round trip.    |
| Gap matrix                       | `docs/gap-vs-zenoh-pico.md`            | Update the "Peer role (mesh)" row from `No - wont` to `Yes` with a note that full mesh forwarding is still deferred.                                            |
| Docs index                       | `docs/README.md`                       | Add PRD-003 row.                                                                                                                                                |
| Docs                             | `docs/prd/prd-003-peer-role.md`        | Add. This PRD.                                                                                                                                                  |

## Edge Cases

- **Cookie replay / malformed OpenSyn.** Server stores the 16-byte random cookie issued in `InitAck` keyed by the accepted `net.Conn`. If `OpenSyn.Cookie` does not match byte-for-byte, close the transport and return `ErrCatProtocol`. Never reuse cookies across accepts (each handshake uses a fresh one).
- **Handshake timeout on the server side.** The server-side handshake driver runs under `context.WithTimeout(ctx, cfg.HandshakeTimeout)` just like the client side. If the remote never sends `OpenSyn`, the accepted transport is closed and the accept loop continues.
- **Two peers with the same ZID.** A peer that receives a JOIN carrying its own ZID ignores it. On accept, if the inbound `InitSyn.ZID` equals our ZID, reject with `ErrCatProtocol` (mirrors `zenoh-pico`).
- **Accept loop drain on `Session.Close`.** `Session.Close` on a peer must stop the accept loop (close the `Listener`), stop the JOIN sender/receiver ticker, close all per-peer sessions, and only then return. Use the same `closeOnce` idiom that exists in v0.2.0 plus a `peerCloseCtx` derived from `ctx` passed to `openPeer`.
- **Listener bind failure.** If every entry in `cfg.Listen` fails to bind (e.g., port in use), `openPeer` returns `ErrCatConnection` wrapping the last error — consistent with how client-mode `Open` treats dial failures.
- **`JOIN` storm on a busy multicast segment.** The JOIN receiver deduplicates by ZID: a repeat JOIN from a ZID we already have a session with is ignored cheaply (no allocation). JOIN is rate-limited on send via `JoinInterval`; we do not rate-limit inbound beyond the dedup.
- **IPv4 multicast interface selection.** Matches the existing `scout.go` behaviour: honour a configured interface if present, otherwise use the OS default. Document that heterogeneous network environments may require an explicit interface.
- **Peer without `Listen`.** `cfg.Role=RolePeer, cfg.Listen=nil` is valid — the peer dials outbound via `Config.Connect` and participates in JOIN multicast but does not accept inbound connections. Useful behind NAT. JOIN announcer still runs (announces presence), but no inbound sessions arrive.
- **`Config.Role=RoleClient` with `Listen` set.** `Listen` is silently ignored in client mode. The log emits a one-time warning at `Open` so users notice misconfiguration.
- **Half-open connections after peer crash.** The existing lease watchdog (`watchdogLoop` in `session.go`) closes any session with no inbound traffic for `3 * lease`. Peer-mode sessions inherit this behaviour unchanged.
- **Subscription fanout across peer sessions.** Out of scope for v0.3.0: each `*Session` handed to `OnPeerConnected` is independent. Application code that wants "publish once, deliver to every connected peer" must iterate its own list of sessions and call `Put` on each. This matches `zenoh-pico`'s peer role, which also relies on explicit per-session publish when not attached to a router.
- **Simultaneous dial of the same peer.** If peer A dials peer B while B's JOIN triggers B to dial A, both ends accept both connections. v0.3.0 keeps both — the application may see two `OnPeerConnected` callbacks for the same remote ZID. Duplicate-session suppression (tiebreak by ZID comparison) is a follow-up.
- **`JoinMsg` with `FlagT` set.** Lease in seconds ⇒ decoder multiplies by 1000. Encoder always emits milliseconds (T clear) for uniformity with `OpenSyn.Encode`.
- **Mismatched protocol version in `InitSyn`.** Server-side handshake rejects any version ≠ `ProtoVersion` with `ErrCatProtocol`, closing the transport.

## Migration

- **Public API is additive.** `Role`, `RoleClient`, `RolePeer`, `PeerHandler`, `Config.Role`, `Config.Listen`, `Config.ScoutAddr`, `Config.JoinInterval`, `Config.OnPeerConnected`, and `DefaultPeerConfig()` are new symbols. Existing v0.2.0 callers that do not set `Role` get `RoleClient` (zero value) and observe identical behaviour.
- **No wire-format breakage.** Client-mode sessions against `zenohd` continue to send `WhatAmI=WhatAmIClientIdx` in `InitSyn`, unchanged from v0.2.0. The wire-level changes in this PRD (`JoinMsg`, server-side `InitSyn`/`OpenSyn` decoders, `HelloMsg.Encode`) are additive; no existing encoder changes its output bytes.
- **Version.** Cut `v0.3.0`. The headline feature is peer role; the JOIN codec and server-side handshake are enabling infrastructure.
- **Downstream users of PRD-001/002.** No source changes required. `*Session` behaves identically whether obtained by dialing a router or by accepting a peer.
- **Gap matrix.** The "Peer role (mesh)" row in `docs/gap-vs-zenoh-pico.md` changes status to `Yes`, with a footnote that full multi-hop mesh forwarding remains deferred.

## Testing

**Unit tests (`go test ./internal/wire/...`).**

- Round-trip `JoinMsg` through `Encode` + `decodeJoin` with: default resolution/batch (S flag clear), non-default (S flag set), short and long ZIDs (1 and 16 bytes), `FlagT` set on decode (verify lease ×1000), extensions present (`FlagZ`).
- Round-trip `InitSyn` through `Encode` + `decodeInitSyn` with the same matrix.
- Round-trip `OpenSyn` through `Encode` + `decodeOpenSyn` with: cookie empty (rejected by encoder as `zenoh-pico` requires non-empty cookies — document and test the rejection), 1-byte cookie, 32-byte cookie, `FlagT` set on decode.
- `HelloMsg.Encode` → `DecodeHello` round trip with 0, 1, and 3 locator strings; ensure `FlagL` is set iff locators are non-empty.
- `WhatAmIIdxToBit(WhatAmIPeerIdx) == WhatAmIPeerBit` and `WhatAmIBitToIdx(WhatAmIPeerBit) == (WhatAmIPeerIdx, true)`; assert `WhatAmIBitToIdx(0x06)` returns `ok=false` (two bits set).
- `DecodeTransport` regression: feed bytes for `InitSyn` (A clear) and assert it returns `*InitSyn` rather than the old "unexpected InitSyn" error.

**Unit tests (`go test ./...` at module root).**

- `peer_test.go:TestTwoPeersLoopback`: start peer A with `Listen=["tcp/127.0.0.1:0"]`; read its bound locator; start peer B with `Connect=[locatorA]`; both register a subscriber on `test/**`; publish from each; assert each receives the other's sample; assert `goleak` clean after both `Close`.
- `peer_test.go:TestServerHandshake`: use a `net.Pipe()` pair; one side runs the client handshake (reusing `Open`'s existing code path), the other runs the server-side handshake from `peer.go`; assert negotiated batch size = min of two, cookie matches byte-for-byte, `*Session` returned on both sides.
- `peer_test.go:TestCookieMismatch`: server issues cookie `[0x01...]`, client sends `OpenSyn` with cookie `[0x02...]`; assert server closes the transport and returns a protocol error.
- `peer_test.go:TestRoleClientDefault`: existing v0.2.0 test suite still passes with `cfg.Role` unset (zero value).
- `peer_test.go:TestPeerWithoutListen`: `Role=RolePeer, Listen=nil, Connect=[loopback peer]`; assert outbound session established and JOIN sender runs.
- `goleak.VerifyTestMain` in `peer_test.go` confirms JOIN sender/receiver, accept loop, and per-session goroutines all terminate on `Close`.

**Python e2e tests (`tests/python-e2e/`).**

- `test_peer_interop.py`: configure `eclipse-zenoh` Python session with `mode = "peer"`, `connect.endpoints = [loopback locator of zenoh-nano-go peer]`. Start the Go peer listening. Python peer subscribes on `test/a`, Go peer publishes a 32-byte payload; assert Python receives bytewise equal payload. Reverse direction: Go peer subscribes, Python peer publishes.
- `test_peer_multicast_join.py` (skip if no multicast loopback): two `zenoh-nano-go` peers on `224.0.0.224:7446` with `Listen` set and `Connect` empty; after `JoinInterval + 500ms`, assert each has at least one `OnPeerConnected` callback.

**Manual interop.**

- Run `examples/z_peer` twice on one host, one with `--listen tcp/0.0.0.0:7447` and one with `--connect tcp/127.0.0.1:7447`; exchange Pub/Sub traffic; tear down both with SIGINT and confirm clean exit.
- Run one `examples/z_peer` and one `zenoh-pico` peer example (`z_pub_peer.c`) on the same host; verify they see each other's JOINs and exchange data.
- Run `examples/z_peer` against the dev router (`tcp/172.26.2.155:31747`) with `Role=RoleClient` explicit to confirm client-mode unchanged.

**Race & leak detection.**

- All `peer_test.go` entries run with `-race`.
- `goleak.VerifyTestMain` in the module-root test package to catch stray JOIN sender/receiver goroutines after `Close`.

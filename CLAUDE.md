# zenoh-nano-go — Claude Code Guide

## Workflow rules

- **Do NOT create git commits automatically.** Always ask the user before committing. Only commit when explicitly instructed.

## Project overview

Pure-Go Zenoh wire protocol client (no CGo). Implements Zenoh protocol `0x09`, client role only. Reference: `zenoh-pico` v1.8.0.

**Module:** `github.com/tasanakorn/zenoh-nano-go`
**Go version:** 1.22+
**External deps:** `golang.org/x/net` (multicast only), `go.uber.org/goleak` (tests only)

## Repository layout

```
.
├── session.go          # Session: Open, Close, reader/writer goroutines
├── config.go           # Config struct and DefaultConfig()
├── publisher.go        # Put, Delete
├── subscriber.go       # DeclareSubscriber, Subscriber, Sample
├── querier.go          # Get, Reply
├── scout.go            # Scout, HelloInfo
├── keyexpr.go          # ValidateKeyExpr
├── zid.go              # ZenohID
├── encoding.go         # Encoding constants
├── errors.go           # ZError, error sentinels
├── internal/
│   ├── wire/           # Binary codec (varint, all message types)
│   ├── session/        # Transport interface, TCP/UDP implementations
│   └── kematch/        # Key expression wildcard matching
├── examples/
│   ├── z_put/          # CLI publisher
│   ├── z_sub/          # CLI subscriber
│   ├── z_get/          # CLI querier
│   ├── z_scout/        # CLI scouting
│   └── z_xport_test/   # Cross-transport smoke test
├── tests/
│   └── python-e2e/     # uv + eclipse-zenoh Python interop tests
└── docs/
    ├── README.md        # Doc index
    └── prd/
        └── prd-001-zenoh-nano-go.md
```

## Build and test

```bash
# Build everything (CGo-free)
CGO_ENABLED=0 go build ./...

# Unit tests
go test ./...

# Python e2e tests (requires live zenohd)
cd tests/python-e2e && uv run pytest -v
```

## Live router (dev environment)

- **TCP:** `tcp/172.26.2.155:31747`
- **Scout:** `224.0.0.224:31746` (UDP multicast)

## Wire protocol notes (hard-won)

These are non-obvious facts discovered via live interop with real zenohd:

1. **Cookie in `InitAck` is varint-prefixed** — NOT u16 LE as the zenoh-pico C source comments suggest. (`internal/wire/codec.go:ReadCookie`)

2. **`InitSyn` S flag** — only set when proposing non-default resolution/batch_size. zenohd mirrors this: its `InitAck` has S=0 when echoing defaults. The decoder must check the S flag before reading resolution+batchSize. (`internal/wire/transport_msg.go:decodeInitAck`)

3. **Put body T flag (0x20) = Timestamp** — zenohd adds a timestamp to every forwarded Put. The timestamp is: NTP u64 varint + ZID as zbytes. Must be skipped before reading payload. (`internal/wire/body.go:DecodePutBody`)

4. **WhatAmI has two different encodings:**
   - Scout `what` field: bitmask (Router=1, Peer=2, Client=4)
   - INIT/HELLO body: 2-bit index (Router=0, Peer=1, Client=2)

5. **Frame payload** — network messages inside a Frame are self-describing (no length prefix, no count field). Read until buffer exhausted.

6. **Session write lifecycle** — `Close()` must close `writeCh` (signaling writerLoop to drain) and wait for the writer to finish BEFORE closing the transport. Otherwise `Put` frames may be dropped.

## Session goroutines

```
writerLoop   — owns write half of transport; exits when writeCh is closed
readerLoop   — owns read half; exits when transport.Close() unblocks ReadFrame()
keepAliveLoop — sends KeepAlive every lease/3
watchdogLoop  — closes session after 3×lease of silence
per-subscriber dispatch goroutines (one per active Subscriber)
```

## Key invariants

- `writeCh` is closed exactly once, via `writeChClose sync.Once`
- `writerDone` channel signals that all pending writes have been flushed
- `enqueue()` blocks (no drop) — backpressure propagates to callers
- Fragment reassembly is keyed by `fragKey{reliable bool}` and only accessed from readerLoop (no mutex needed)
- `snMask` is set from the negotiated `Resolution` field in `InitAck`

## Adding a new message type

1. Add encode/decode to the appropriate file in `internal/wire/`
2. Add a round-trip test in `*_test.go` alongside it
3. Wire it into `DecodeNetworkStream` or `DecodeTransport` dispatch
4. Handle it in `session.go:dispatchNetworkPayload` or `dispatchTransport`

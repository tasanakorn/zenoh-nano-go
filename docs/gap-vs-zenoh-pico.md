# Gap vs zenoh-pico v1.8.0

How `zenoh-nano-go` v0.1.0 compares to [`zenoh-pico`](https://github.com/eclipse-zenoh/zenoh-pico) v1.8.0 for each zenoh-pico feature.

Protocol version for both libraries: `0x09`.

## Legend

| nano-go? value | Meaning                                                                          |
| -------------- | -------------------------------------------------------------------------------- |
| Yes            | Implemented in v0.1.0                                                            |
| No - wont      | Permanently out of scope (see [PRD-001](prd/prd-001-zenoh-nano-go.md#non-goals)) |
| No - defer     | Not in v0.1.0; candidate for a later milestone. No commitment, no timeline       |
| No - oot       | Out of target scope (peer/router role infrastructure, etc.)                      |

## 1. Transports

| zenoh-pico feature       | pico? | nano-go?   | Notes                                                        |
| ------------------------ | ----- | ---------- | ------------------------------------------------------------ |
| TCP unicast (client)     | Yes   | Yes        | 2-byte LE length prefix, `TCP_NODELAY`                       |
| UDP unicast (client)     | Yes   | Yes        | Datagram = one transport message, best-effort                |
| UDP multicast scouting   | Yes   | Yes        | `224.0.0.224:7446` default, configurable                     |
| Multicast data transport | Yes   | No - defer | Scope creep for v0.1; unicast covers target deployments      |
| Serial / UART            | Yes   | No - wont  | Microcontroller-oriented; TinyGo territory                   |
| Bluetooth LE             | Yes   | No - wont  | Microcontroller-oriented                                     |
| Raw Ethernet             | Yes   | No - wont  | Not useful for Go gateway/sidecar; requires kernel privilege |
| Shared-memory (SHM)      | Yes   | No - wont  | Requires CGo / platform-specific primitives                  |
| TLS / QUIC               | Yes   | No - defer | Requires cert plumbing, separate Transport implementation    |
| WebSocket                | Yes   | No - defer | Additive; not on the critical path for v0.1                  |
| Unix domain sockets      | Yes   | No - defer | Additive; straightforward once Transport interface is stable |

## 2. Roles

| zenoh-pico feature   | pico? | nano-go?  | Notes                                    |
| -------------------- | ----- | --------- | ---------------------------------------- |
| Client role          | Yes   | Yes       | Only supported role                      |
| Peer role (mesh)     | Yes   | No - wont | Out of scope; client-only by design      |
| Router / broker role | Yes   | No - wont | Out of scope                             |

## 3. Discovery / Scouting

| zenoh-pico feature | pico? | nano-go?   | Notes                                                        |
| ------------------ | ----- | ---------- | ------------------------------------------------------------ |
| Multicast scout    | Yes   | Yes        | UDP `224.0.0.224:31746`; HelloInfo returned from `Scout()`   |
| Gossip scouting    | Yes   | No - wont  | Requires peer role; permanently excluded                     |
| Interest messages  | Yes   | No - defer | Not required for client role; zenohd tolerates absence       |

## 4. Session / Handshake

| zenoh-pico feature                             | pico? | nano-go? | Notes                                                         |
| ---------------------------------------------- | ----- | -------- | ------------------------------------------------------------- |
| InitSyn / InitAck (varint-prefixed cookie)     | Yes   | Yes      | Cookie encoding clarified vs. zenoh-pico C source comments    |
| OpenSyn / OpenAck                              | Yes   | Yes      |                                                               |
| Negotiated batch_size and resolution           | Yes   | Yes      | S flag only set when proposing non-defaults                   |
| KeepAlive (lease/3 cadence)                    | Yes   | Yes      |                                                               |
| Lease watchdog (3 x lease silence -> close)    | Yes   | Yes      |                                                               |
| Graceful Close (drain writer before transport) | Yes   | Yes      | `writeCh` drained via `writerDone` before `transport.Close()` |

## 5. Messaging primitives

| zenoh-pico feature                    | pico? | nano-go?   | Notes                                                          |
| ------------------------------------- | ----- | ---------- | -------------------------------------------------------------- |
| Put                                   | Yes   | Yes        |                                                                |
| Delete                                | Yes   | Yes        |                                                                |
| Subscribe (callback-based)            | Yes   | Yes        |                                                                |
| Get / Query / Reply (slice-returning) | Yes   | Yes        |                                                                |
| Publisher handle (`DeclarePublisher`) | Yes   | Yes        |                                                                |
| Querier handle (`DeclareQuerier`)     | Yes   | Yes        |                                                                |
| Queryable (`DeclareQueryable`)        | Yes   | Yes        | Implemented in v0.2.0; auto-close via dispatch goroutine       |
| Liveliness tokens                     | Yes   | No - defer | Separate declaration family; niche for a v0.1 client           |
| Admin space queries                   | Yes   | No - defer | Convenience layer over existing Get; not on the critical path  |

## 6. Frames and fragmentation

| zenoh-pico feature                     | pico? | nano-go?   | Notes                                                          |
| -------------------------------------- | ----- | ---------- | -------------------------------------------------------------- |
| Frame (reliable + best-effort)         | Yes   | Yes        |                                                                |
| Fragment reassembly                    | Yes   | Yes        | Keyed by `fragKey{reliable bool}`; only from readerLoop        |
| Batching (multiple messages per Frame) | Yes   | No - defer | One network message per Frame today; simple and correct        |
| UDP reliability (per-frame retransmit) | Yes   | No - defer | SN implemented; retransmit not wired; UDP is best-effort today |

## 7. Declarations

| zenoh-pico feature | pico? | nano-go?   | Notes                                                       |
| ------------------ | ----- | ---------- | ----------------------------------------------------------- |
| DECL_KEXPR         | Yes   | Yes        | Encode + decode; not auto-issued (string form on wire)      |
| UNDECL_KEXPR       | Yes   | Yes        | Encode + decode; not auto-issued                            |
| DECL_SUBSCRIBER    | Yes   | Yes        | Used by `DeclareSubscriber`                                 |
| UNDECL_SUBSCRIBER  | Yes   | Yes        | Used by `Subscriber.Undeclare`                              |
| DECL_FINAL         | Yes   | Yes        | Acked on handshake                                          |
| DECL_QUERYABLE     | Yes   | Yes        | Used by `DeclareQueryable` (v0.2.0)                         |
| UNDECL_QUERYABLE   | Yes   | Yes        | Used by `Queryable.Undeclare` (v0.2.0)                      |
| DECL_LIVELINESS    | Yes   | No - defer | Blocked on Liveliness feature (section 5)                   |

## 8. Key expressions

| zenoh-pico feature                     | pico? | nano-go?   | Notes                                          |
| -------------------------------------- | ----- | ---------- | ---------------------------------------------- |
| String-form wire encoding (`N` flag)   | Yes   | Yes        |                                                |
| Numeric-form wire encoding (decode)    | Yes   | Yes        |                                                |
| `ValidateKeyExpr`                      | Yes   | Yes        | Rejects empty chunks, `//`                     |
| Wildcard matching (`*`, `**`)          | Yes   | Yes        | `internal/kematch`                             |
| Selector split (`key?params`)          | Yes   | Yes        | `SplitSelector`                                |
| Numeric-form wire encoding (send side) | Yes   | No - defer | Always send string form; optimization only     |

## 9. QoS and extensions

| zenoh-pico feature                          | pico? | nano-go?   | Notes                                                 |
| ------------------------------------------- | ----- | ---------- | ----------------------------------------------------- |
| Timestamp decode (T flag 0x20 on Put)       | Yes   | Yes        | Decoded and skipped on forwarded Put                  |
| Extensions: Unit / ZInt / ZBuf (skip unk.)  | Yes   | Yes        | Unknown non-mandatory extensions skipped safely       |
| Congestion-control hints                    | Yes   | No - defer | Extension codec exists; not plumbed through API       |
| Priority hints                              | Yes   | No - defer | Extension codec exists; not plumbed through API       |
| Express flag on Put                         | Yes   | No - defer | Single reliability class on send today                |
| Reliability selector on Put                 | Yes   | No - defer | Single reliability class on send today                |
| Compression extension                       | Yes   | No - defer | Not on the critical path for v0.1                     |

## 10. Security

| zenoh-pico feature                    | pico? | nano-go?   | Notes                                                       |
| ------------------------------------- | ----- | ---------- | ----------------------------------------------------------- |
| Authentication extension (user / PSK) | Yes   | No - defer | Extension codec exists; negotiation flow is additional work |
| Access-control extension              | Yes   | No - defer | Protocol extension; not wired through handshake             |

## 11. Build and portability

| zenoh-pico feature                       | pico? | nano-go?  | Notes                                                       |
| ---------------------------------------- | ----- | --------- | ----------------------------------------------------------- |
| Build-time feature flags (`Z_FEATURE_*`) | Yes   | No - wont | Library is always the same shape; deliberate simplification |

## Summary vs zenoh-pico

`zenoh-pico` v1.8.0 targets microcontrollers: it includes serial / BLE / raw-Ethernet transports, peer-role gossip, and a fine-grained build-time feature matrix (`Z_FEATURE_*`). `zenoh-nano-go` deliberately trades those for a smaller surface aimed at Go services and gateways:

- **Narrower transport set** — TCP / UDP unicast + multicast scouting only. No serial, BLE, or raw Ethernet.
- **Client role only** — no peer mesh, no gossip.
- **No build-time feature flags** — the library is always the same shape; `CGO_ENABLED=0` is the only toggle that matters.
- **Idiomatic Go surface** — callback-based subscribers, slice-returning `Get`, explicit `Close`, goroutine-leak tested.

Interoperability with `zenohd` and with `zenoh-pico` peers over TCP / UDP unicast is a first-class goal and is verified by the Python e2e suite and the cross-transport smoke test.

## Change log

| Date       | Change                                                                              |
| ---------- | ----------------------------------------------------------------------------------- |
| 2026-04-15 | v0.2.0: Queryable, DECL_QUERYABLE, UNDECL_QUERYABLE now Yes                         |
| 2026-04-15 | Refocused: removed parity rows; kept only features pico has that nano-go lacks      |
| 2026-04-15 | Restructured as pico-first feature matrix                                           |
| 2026-04-15 | Initial document against v0.1.0 scope                                               |

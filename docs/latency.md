# Expected Latency

Benchmark results for `zenoh-nano-go` measuring start-to-put latency — the time
from a cold `zenoh.Open()` call to a successful `s.Put()`.

## Test setup

- **Tool:** `examples/z_latency` (`go run ./examples/z_latency/ -pyrouter -n 10`)
- **Remote router:** `tcp/172.26.2.155:31747` (LAN, ~1 ms RTT)
- **Local Python router:** `tcp/127.0.0.1:17447` (loopback, started by the tool)
- **Go version:** 1.22, `CGO_ENABLED=0`
- **Platform:** Linux x86-64

Each trial opens a fresh session, calls `Put`, then closes the session.
`open` and `put` are timed independently.

## Results

### Direct TCP (no scout)

The application has the router address hardcoded.

| metric | open   | put   | total  |
| ------ | ------ | ----- | ------ |
| min    | 2.2 ms | 10 µs | 2.2 ms |
| avg    | 4.5 ms | 16 µs | 4.5 ms |
| p95    | 10 ms  | 25 µs | 10 ms  |

- `open` dominates total latency — it covers TCP connect + INIT/OPEN handshake (two round trips).
- `put` is negligible (~15 µs): encode + write + kernel flush.
- p95 variance (~2× avg) comes from network jitter on the remote router leg.

### Scout → local Python router

The application discovers the router via multicast UDP before connecting.
Uses `ReturnOnFirst: true` to return as soon as the first Hello arrives.

| metric | scout  | open   | put   | total  |
| ------ | ------ | ------ | ----- | ------ |
| min    | 0.7 ms | 1.4 ms | 9 µs  | 2.2 ms |
| avg    | 1.7 ms | 1.7 ms | 19 µs | 3.4 ms |
| p95    | 4.5 ms | 2.2 ms | 46 µs | 5.9 ms |

- Scout round-trip to a local router is **under 2 ms** in the typical case.
- `open` to loopback is faster than to the remote router (1.7 ms vs 4.5 ms).
- Combined, scout + open + put is **comparable to direct TCP** to a remote router.

### Scout → remote router (LAN)

| metric | scout  | open   | put   | total  |
| ------ | ------ | ------ | ----- | ------ |
| min    | 0.8 ms | 1.5 ms | 10 µs | 3.1 ms |
| avg    | 3.2 ms | 2.6 ms | 15 µs | 5.8 ms |
| p95    | 5.3 ms | 3.9 ms | 18 µs | 7.9 ms |

- Scout success rate was 70% (3/10 trials received a UDP-only Hello from another node first).
- When scout succeeds the latency is similar to direct TCP.

## Summary

| mode                       | total avg | total p95 |
| -------------------------- | --------- | --------- |
| Direct TCP (remote router) | 4.5 ms    | 10 ms     |
| Scout + local router       | 3.4 ms    | 5.9 ms    |
| Scout + remote router      | 5.8 ms    | 7.9 ms    |

## Design guidance

**Use direct TCP** when:
- The router address is known at deploy time.
- You want the simplest, most predictable latency.
- Target: **2–5 ms** typical, up to ~10 ms at p95 over LAN.

**Use scout (`ReturnOnFirst: true`)** when:
- The router address is not known in advance (dynamic infrastructure).
- The router is on the same host or LAN segment — scout round-trip stays under 2 ms.
- Accept ~1–2 ms of added latency for discovery.

**Avoid scout with `ReturnOnFirst: false`** (the default) for latency-sensitive
startup paths — it always waits out the full `Timeout` even after the first Hello
arrives. Reserve it for enumerating all peers on the network.

## Scout behavior note

`Scout` collects all Hello replies until `Timeout` expires. Set
`ReturnOnFirst: true` in `ScoutOptions` to return immediately on the first Hello:

```go
hellos, err := zenoh.Scout(ctx, &zenoh.ScoutOptions{
    Timeout:       500 * time.Millisecond,
    ReturnOnFirst: true,
})
```

Without `ReturnOnFirst`, every scout call costs at least `Timeout` regardless of
how quickly the router replies.

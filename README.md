# zenoh-nano-go

A pure-Go Zenoh protocol client — no CGo, no C dependencies.

Implements [Zenoh](https://zenoh.io) wire protocol `0x09` (client role only), using [zenoh-pico](https://github.com/eclipse-zenoh/zenoh-pico) as the reference for minimal feature scope. Interoperates with `zenohd` and `zenoh-pico` peers out of the box.

```
go get github.com/tasanakorn/zenoh-nano-go
```

## Features

| Feature              | Status |
| -------------------- | ------ |
| TCP unicast          | v0.1.0 |
| UDP unicast          | v0.1.0 |
| UDP multicast scout  | v0.1.0 |
| Publish (Put/Delete) | v0.1.0 |
| Subscribe            | v0.1.0 |
| Get (query/reply)    | v0.1.0 |
| Fragment reassembly  | v0.1.0 |
| CGo-free             | always |

## Quick start

```go
package main

import (
    "context"
    "fmt"

    zenoh "github.com/tasanakorn/zenoh-nano-go"
)

func main() {
    cfg := zenoh.DefaultConfig() // connects to tcp/127.0.0.1:7447
    s, err := zenoh.Open(context.Background(), cfg)
    if err != nil { panic(err) }
    defer s.Close()

    // Subscribe
    sub, _ := s.DeclareSubscriber("demo/**", func(sample zenoh.Sample) {
        fmt.Printf("%s: %s\n", sample.KeyExpr, sample.Payload)
    })
    defer sub.Undeclare()

    // Publish
    s.Put("demo/hello", []byte("world"))
}
```

## Scout

```go
hellos, err := zenoh.Scout(ctx, &zenoh.ScoutOptions{
    MulticastAddr: "224.0.0.224:7446",
    Timeout:       3 * time.Second,
})
for _, h := range hellos {
    fmt.Println(h.WhatAmI, h.ZID, h.Locators)
}
```

## Configuration

```go
cfg := &zenoh.Config{
    Connect:          []string{"tcp/192.168.1.5:7447"},
    Lease:            10 * time.Second,
    HandshakeTimeout: 5 * time.Second,
}
```

## Examples

```
go run ./examples/z_put   -e tcp/host:7447 -k demo/key -v "hello"
go run ./examples/z_sub   -e tcp/host:7447 -k "demo/**"
go run ./examples/z_get   -e tcp/host:7447 -k "demo/**"
go run ./examples/z_scout -a 224.0.0.224:7446 -t 3s
```

## Design

Four internal layers, each independently testable:

```
Public API       Open, Put, Delete, DeclareSubscriber, Get, Scout
Session layer    handshake, keepalive, lease watchdog, fragment reassembly
Transport layer  TCP (2-byte LE length prefix) / UDP (datagram)
Wire codec       LEB128 varint, extensions, all message types
```

See [docs/](docs/) for the full design documents.

## Tested against

- `zenohd` (Rust router, protocol `0x09`)
- `eclipse-zenoh/zenoh-python` (Python bindings, cross-language pub/sub)
- `zenoh-pico` peers (TCP + UDP unicast)

Wire format corrections discovered during live testing:

- Cookie in `InitAck` is varint-prefixed (not u16 LE)
- `InitSyn` S flag set only for non-default resolution/batch_size
- Forwarded `Put` bodies carry a timestamp (T flag `0x20`) that must be skipped

## Non-goals (v0.1.0)

- Peer / router / broker roles
- Liveliness, admin space, raw-Ethernet / serial / BLE
- TinyGo (no gratuitous blockers, but not tested)
- Full parity with [`eclipse-zenoh/zenoh-go`](https://github.com/eclipse-zenoh/zenoh-go) (CGo binding)

## License

Apache-2.0

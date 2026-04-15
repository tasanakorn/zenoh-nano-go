package zenoh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/tasanakorn/zenoh-nano-go/internal/wire"
	"golang.org/x/net/ipv4"
)

// HelloInfo describes a Hello announcement received during scouting.
type HelloInfo struct {
	WhatAmI  WhatAmI
	ZID      ZenohID
	Locators []string
}

// WhatAmI identifies the role of a Zenoh node.
type WhatAmI int

const (
	WhatAmIRouter WhatAmI = iota
	WhatAmIPeer
	WhatAmIClient
)

// DefaultScoutAddr is the default Zenoh IPv4 multicast address.
const DefaultScoutAddr = "224.0.0.224:7446"

// ScoutOptions configures the Scout call.
type ScoutOptions struct {
	// MulticastAddr is the "host:port" multicast group to use.
	MulticastAddr string
	// What is the Scout role bitmask (Router=1, Peer=2, Client=4). If 0, defaults to router|peer.
	What uint8
	// Timeout is the total time to wait for hellos.
	Timeout time.Duration
	// ReturnOnFirst stops collecting after the first Hello is received.
	// Useful when only one router is expected and minimal latency matters.
	ReturnOnFirst bool
}

// Scout sends a SCOUT and collects HelloInfo replies until timeout expires.
func Scout(ctx context.Context, opts *ScoutOptions) ([]HelloInfo, error) {
	if opts == nil {
		opts = &ScoutOptions{}
	}
	if opts.MulticastAddr == "" {
		opts.MulticastAddr = DefaultScoutAddr
	}
	if opts.What == 0 {
		opts.What = wire.WhatAmIRouterBit | wire.WhatAmIPeerBit
	}
	if opts.Timeout == 0 {
		opts.Timeout = 3 * time.Second
	}

	gaddr, err := net.ResolveUDPAddr("udp4", opts.MulticastAddr)
	if err != nil {
		return nil, wrapErr(ErrCatInvalidArg, "resolve multicast addr", err)
	}

	// Listen on an arbitrary UDP port; join the multicast group.
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, wrapErr(ErrCatConnection, "listen udp", err)
	}
	defer conn.Close()

	p := ipv4.NewPacketConn(conn)
	if ifaces, ierr := net.Interfaces(); ierr == nil {
		for _, ifi := range ifaces {
			if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
				continue
			}
			_ = p.JoinGroup(&ifi, gaddr)
		}
	}
	_ = p.SetMulticastLoopback(true)

	zid := NewRandomZID()
	scoutMsg := &wire.ScoutMsg{
		Version: wire.ProtoVersion,
		What:    opts.What,
		ZID:     zid.Bytes(),
	}
	data := scoutMsg.Encode()
	if _, err := conn.WriteTo(data, gaddr); err != nil {
		return nil, wrapErr(ErrCatConnection, "send scout", err)
	}

	deadline := time.Now().Add(opts.Timeout)
	_ = conn.SetReadDeadline(deadline)

	buf := make([]byte, 65536)
	var hellos []HelloInfo
	seen := map[string]bool{}
	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				if len(hellos) == 0 {
					return nil, ctx.Err()
				}
				return hellos, nil
			default:
			}
		}
		_ = conn.SetReadDeadline(deadline)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if len(hellos) == 0 {
					return nil, ErrScoutTimeout
				}
				return hellos, nil
			}
			return hellos, wrapErr(ErrCatConnection, "read hello", err)
		}
		if n < 1 {
			continue
		}
		hdr := buf[0]
		if wire.MsgID(hdr) != wire.MidHello {
			continue
		}
		hello, err := wire.DecodeHello(buf[:n])
		if err != nil {
			continue
		}
		pz, err := ZIDFromBytes(hello.ZID)
		if err != nil {
			continue
		}
		key := pz.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		hellos = append(hellos, HelloInfo{
			WhatAmI:  whatAmIFromIdx(hello.WhatAmI),
			ZID:      pz,
			Locators: hello.Locators,
		})
		if opts.ReturnOnFirst {
			return hellos, nil
		}
	}
}

func whatAmIFromIdx(idx uint8) WhatAmI {
	switch idx {
	case wire.WhatAmIRouterIdx:
		return WhatAmIRouter
	case wire.WhatAmIPeerIdx:
		return WhatAmIPeer
	case wire.WhatAmIClientIdx:
		return WhatAmIClient
	}
	return WhatAmIPeer
}

// String returns a human-readable role name.
func (w WhatAmI) String() string {
	switch w {
	case WhatAmIRouter:
		return "router"
	case WhatAmIPeer:
		return "peer"
	case WhatAmIClient:
		return "client"
	}
	return fmt.Sprintf("WhatAmI(%d)", int(w))
}

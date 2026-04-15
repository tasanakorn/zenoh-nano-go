package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	zenoh "github.com/tasanakorn/zenoh-nano-go"
)

func main() {
	endpoint := flag.String("e", "tcp/127.0.0.1:7447", "endpoint")
	selector := flag.String("s", "demo/**", "selector (keyexpr[?params])")
	timeout := flag.Duration("t", 3*time.Second, "query timeout")
	flag.Parse()

	cfg := zenoh.DefaultConfig()
	cfg.Connect = []string{*endpoint}

	s, err := zenoh.Open(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	replies, err := s.Get(ctx, *selector)
	if err != nil && err != context.DeadlineExceeded {
		log.Fatal(err)
	}
	for i, r := range replies {
		if r.Err != nil {
			fmt.Printf("[%d] ERR %s: %s\n", i, r.KeyExpr, string(r.Payload))
			continue
		}
		fmt.Printf("[%d] %s : %s\n", i, r.KeyExpr, string(r.Payload))
	}
	fmt.Printf("Received %d replies.\n", len(replies))
}

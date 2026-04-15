package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	zenoh "github.com/tasanakorn/zenoh-nano-go"
)

func main() {
	endpoint := flag.String("e", "tcp/127.0.0.1:7447", "endpoint")
	key := flag.String("k", "demo/**", "key expression pattern")
	flag.Parse()

	cfg := zenoh.DefaultConfig()
	cfg.Connect = []string{*endpoint}

	s, err := zenoh.Open(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	sub, err := s.DeclareSubscriber(*key, func(sample zenoh.Sample) {
		fmt.Printf(">> %s : %s\n", sample.KeyExpr, string(sample.Payload))
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Undeclare()

	fmt.Printf("Subscribed to '%s'. Press Ctrl+C to exit.\n", *key)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}

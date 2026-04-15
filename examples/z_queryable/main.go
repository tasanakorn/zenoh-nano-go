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
	key := flag.String("k", "demo/example/queryable", "key expression to serve")
	flag.Parse()

	cfg := zenoh.DefaultConfig()
	cfg.Connect = []string{*endpoint}

	s, err := zenoh.Open(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	qa, err := s.DeclareQueryable(*key, func(q *zenoh.Query) {
		fmt.Printf(">> [Queryable] Received query '%s'\n", q.KeyExpr())
		defer q.Close() // always terminate the response stream
		if err := q.Reply(q.KeyExpr(), []byte("Queryable reply: "+q.KeyExpr())); err != nil {
			log.Printf("Reply error: %v", err)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	defer qa.Close()

	fmt.Printf("Queryable on '%s' (Ctrl+C to quit)\n", *key)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}

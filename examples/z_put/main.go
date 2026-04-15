package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	zenoh "github.com/tasanakorn/zenoh-nano-go"
)

func main() {
	endpoint := flag.String("e", "tcp/127.0.0.1:7447", "endpoint")
	key := flag.String("k", "demo/hello", "key expression")
	value := flag.String("v", "Hello, zenoh!", "value")
	flag.Parse()

	cfg := zenoh.DefaultConfig()
	cfg.Connect = []string{*endpoint}

	s, err := zenoh.Open(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	if err := s.Put(*key, []byte(*value)); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Put '%s' = '%s'\n", *key, *value)
}

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
	timeout := flag.Duration("t", 3*time.Second, "scout timeout")
	addr := flag.String("a", zenoh.DefaultScoutAddr, "multicast address")
	flag.Parse()

	hellos, err := zenoh.Scout(context.Background(), &zenoh.ScoutOptions{
		MulticastAddr: *addr,
		Timeout:       *timeout,
	})
	if err != nil {
		log.Fatal(err)
	}
	for i, h := range hellos {
		fmt.Printf("[%d] %s zid=%s locators=%v\n", i, h.WhatAmI, h.ZID, h.Locators)
	}
	fmt.Printf("%d hello(s) received.\n", len(hellos))
}

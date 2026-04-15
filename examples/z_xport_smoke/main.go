// z_xport_smoke opens a subscriber via one transport and a publisher via another,
// verifying that messages flow through the router between transports.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	zenoh "github.com/tasanakorn/zenoh-nano-go"
)

func main() {
	router := flag.String("r", "172.26.2.155:31747", "router host:port")
	key := flag.String("k", "test/xport", "key expression to use")
	flag.Parse()

	tcp := "tcp/" + *router
	udp := "udp/" + *router

	received := make(chan string, 8)

	// --- Subscriber via TCP ---
	subCfg := zenoh.DefaultConfig()
	subCfg.Connect = []string{tcp}
	subSess, err := zenoh.Open(context.Background(), subCfg)
	if err != nil {
		log.Fatalf("sub Open (TCP): %v", err)
	}
	defer subSess.Close()

	sub, err := subSess.DeclareSubscriber(*key, func(s zenoh.Sample) {
		received <- fmt.Sprintf("[sub-TCP] %s = %q", s.KeyExpr, string(s.Payload))
	})
	if err != nil {
		log.Fatalf("DeclareSubscriber: %v", err)
	}
	defer sub.Undeclare()
	fmt.Printf("Subscriber open via TCP (%s), key=%s\n\n", tcp, *key)
	time.Sleep(300 * time.Millisecond) // let declare propagate

	type testCase struct {
		label    string
		locator  string
		payload  string
	}
	cases := []testCase{
		{"TCP→TCP", tcp, "hello from TCP publisher"},
		{"UDP→TCP", udp, "hello from UDP publisher"},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		timeout := time.After(10 * time.Second)
		got := map[string]bool{}
		for {
			select {
			case msg := <-received:
				fmt.Printf("  RECV %s\n", msg)
				for _, tc := range cases {
					if strings.Contains(msg, tc.payload) {
						got[tc.label] = true
					}
				}
				if len(got) == len(cases) {
					fmt.Println("\nAll messages received across both transports.")
					return
				}
			case <-timeout:
				fmt.Println("\nTimeout — not all messages arrived:")
				for _, tc := range cases {
					mark := "MISSING"
					if got[tc.label] { mark = "ok" }
					fmt.Printf("  %s: %s\n", tc.label, mark)
				}
				return
			}
		}
	}()

	for _, tc := range cases {
		fmt.Printf("Publishing via %s (%s)...\n", tc.label[:strings.Index(tc.label, "→")], tc.locator)
		pubCfg := zenoh.DefaultConfig()
		pubCfg.Connect = []string{tc.locator}
		sess, err := zenoh.Open(context.Background(), pubCfg)
		if err != nil {
			log.Printf("  Open %s: %v (skipping)", tc.label, err)
			continue
		}
		if err := sess.Put(*key, []byte(tc.payload)); err != nil {
			log.Printf("  Put %s: %v", tc.label, err)
		} else {
			fmt.Printf("  Put: %q\n", tc.payload)
		}
		sess.Close()
		time.Sleep(200 * time.Millisecond)
	}

	wg.Wait()
}

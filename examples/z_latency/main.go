// z_latency measures start-to-put latency across three modes:
//
//	direct       — Open(tcp) → Put, no discovery overhead
//	scout        — Scout(multicast) → Open → Put  (remote router)
//	python-scout — Scout(multicast) → Open → Put  (local Python router, started automatically)
//
// Each trial measures:
//
//	open   — TCP handshake + INIT/OPEN exchange
//	put    — encode + write + flush
//	scout  — (scout modes only) multicast → first Hello received
//	total  — all phases combined
//
// Note: zenoh-nano-go is client mode only; scout always discovers a router.
//
// Usage:
//
//	go run ./examples/z_latency                    # direct + remote-router scout
//	go run ./examples/z_latency -pyrouter          # also run Python-router scout
//	go run ./examples/z_latency -r 127.0.0.1:7447 -n 20
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	zenoh "github.com/tasanakorn/zenoh-nano-go"
)

func main() {
	router       := flag.String("r", "172.26.2.155:31747", "router host:port for direct mode")
	scoutAddr    := flag.String("scout", "224.0.0.224:31746", "multicast scout address for remote router")
	key          := flag.String("k", "test/latency", "key expression")
	n            := flag.Int("n", 10, "number of trials per mode")
	pyRouter     := flag.Bool("pyrouter", false, "also run Python-router scout mode")
	routerScript := flag.String("routerscript", "examples/z_latency/router.py", "path to router.py")
	routerPkg    := flag.String("routerpkg", "tests/python-e2e", "uv project dir that has zenoh installed")
	flag.Parse()

	fmt.Printf("Trials: %d   key: %s\n\n", *n, *key)

	fmt.Println("=== Mode: direct (tcp, no scout) ===")
	runDirect(*router, *key, *n)

	fmt.Println()
	fmt.Println("=== Mode: scout + connect (remote router) ===")
	runScout(*scoutAddr, *key, *n, 2*time.Second)

	if *pyRouter {
		fmt.Println()
		fmt.Println("=== Mode: scout + connect (Python router, local) ===")
		runWithPythonRouter(*routerScript, *routerPkg, *key, *n)
	}
}

// runDirect measures Open→Put with a hardcoded TCP locator.
func runDirect(router, key string, n int) {
	locator := "tcp/" + router
	var opens, puts, totals []time.Duration

	for i := 0; i < n; i++ {
		t0 := time.Now()

		cfg := zenoh.DefaultConfig()
		cfg.Connect = []string{locator}
		s, err := zenoh.Open(context.Background(), cfg)
		if err != nil {
			log.Printf("  trial %d: Open failed: %v", i+1, err)
			continue
		}
		tOpen := time.Since(t0)

		tPut0 := time.Now()
		if err := s.Put(key, []byte(fmt.Sprintf("trial-%d", i+1))); err != nil {
			log.Printf("  trial %d: Put failed: %v", i+1, err)
			s.Close()
			continue
		}
		tPut := time.Since(tPut0)
		s.Close()

		total := tOpen + tPut
		opens = append(opens, tOpen)
		puts = append(puts, tPut)
		totals = append(totals, total)
		fmt.Printf("  [%2d] open=%-8s put=%-8s total=%s\n",
			i+1,
			tOpen.Round(time.Microsecond),
			tPut.Round(time.Microsecond),
			total.Round(time.Microsecond),
		)
	}

	fmt.Println()
	printStats("open ", opens)
	printStats("put  ", puts)
	printStats("total", totals)
}

// runScout measures Scout→Open→Put with multicast discovery.
func runScout(scoutAddr, key string, n int, timeout time.Duration) {
	var scouts, opens, puts, totals []time.Duration

	for i := 0; i < n; i++ {
		t0 := time.Now()
		hellos, err := zenoh.Scout(context.Background(), &zenoh.ScoutOptions{
			MulticastAddr: scoutAddr,
			Timeout:       timeout,
			ReturnOnFirst: true,
		})
		tScout := time.Since(t0)

		if err != nil || len(hellos) == 0 {
			log.Printf("  trial %d: Scout: no routers found (err=%v, hellos=%d)", i+1, err, len(hellos))
			continue
		}

		locator := firstTCPLocator(hellos)
		if locator == "" {
			log.Printf("  trial %d: no TCP locator in hellos %+v", i+1, hellos)
			continue
		}

		tOpen0 := time.Now()
		cfg := zenoh.DefaultConfig()
		cfg.Connect = []string{locator}
		s, err := zenoh.Open(context.Background(), cfg)
		if err != nil {
			log.Printf("  trial %d: Open failed: %v", i+1, err)
			continue
		}
		tOpen := time.Since(tOpen0)

		tPut0 := time.Now()
		if err := s.Put(key, []byte(fmt.Sprintf("trial-%d", i+1))); err != nil {
			log.Printf("  trial %d: Put failed: %v", i+1, err)
			s.Close()
			continue
		}
		tPut := time.Since(tPut0)
		s.Close()

		total := tScout + tOpen + tPut
		scouts = append(scouts, tScout)
		opens = append(opens, tOpen)
		puts = append(puts, tPut)
		totals = append(totals, total)
		fmt.Printf("  [%2d] scout=%-8s open=%-8s put=%-8s total=%s\n",
			i+1,
			tScout.Round(time.Microsecond),
			tOpen.Round(time.Microsecond),
			tPut.Round(time.Microsecond),
			total.Round(time.Microsecond),
		)
	}

	fmt.Println()
	printStats("scout", scouts)
	printStats("open ", opens)
	printStats("put  ", puts)
	printStats("total", totals)
}

// runWithPythonRouter starts a Python zenoh router subprocess, waits for it
// to be ready, then runs scout→open→put trials against it.
func runWithPythonRouter(routerScript, routerPkg, key string, n int) {
	absScript, err := absPath(routerScript)
	if err != nil {
		log.Printf("  pyrouter: cannot resolve script path: %v", err)
		return
	}

	fmt.Printf("  Starting Python router: %s\n", absScript)
	cmd := exec.Command("uv", "run", "--project", routerPkg, "python", absScript)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("  pyrouter: stdout pipe: %v", err)
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("  pyrouter: stdin pipe: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("  pyrouter: start failed: %v", err)
		return
	}
	defer func() {
		stdin.Close()
		_ = cmd.Wait()
	}()

	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "READY" {
				close(ready)
				return
			}
		}
	}()
	select {
	case <-ready:
		fmt.Println("  Python router ready.\n")
	case <-time.After(10 * time.Second):
		log.Println("  pyrouter: timed out waiting for READY")
		return
	}

	// Use standard Zenoh multicast port — router.py listens on 224.0.0.224:7446.
	runScout("224.0.0.224:7446", key, n, 500*time.Millisecond)
}

func absPath(p string) (string, error) {
	if len(p) > 0 && p[0] == '/' {
		return p, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd + "/" + p, nil
}

// firstTCPLocator returns the first tcp/ locator found in any hello.
func firstTCPLocator(hellos []zenoh.HelloInfo) string {
	for _, h := range hellos {
		for _, loc := range h.Locators {
			if len(loc) > 4 && loc[:4] == "tcp/" {
				return loc
			}
		}
	}
	return ""
}

// printStats prints min/avg/p95/max for a slice of durations.
func printStats(label string, d []time.Duration) {
	if len(d) == 0 {
		fmt.Printf("  %s: no data\n", label)
		return
	}
	sorted := make([]time.Duration, len(d))
	copy(sorted, d)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum float64
	for _, v := range d {
		sum += float64(v)
	}
	avg := time.Duration(sum / float64(len(d)))
	p95 := sorted[int(math.Ceil(float64(len(sorted))*0.95))-1]

	fmt.Printf("  %s  n=%-3d  min=%-8s  avg=%-8s  p95=%-8s  max=%s\n",
		label, len(d),
		sorted[0].Round(time.Microsecond),
		avg.Round(time.Microsecond),
		p95.Round(time.Microsecond),
		sorted[len(sorted)-1].Round(time.Microsecond),
	)
}

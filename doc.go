// Package zenoh provides a pure-Go client for the Zenoh publish-subscribe-query protocol.
// It implements Zenoh wire protocol version 0x09 in client mode with no CGo dependencies.
//
// Quick start:
//
//	s, err := zenoh.Open(ctx, zenoh.DefaultConfig())
//	if err != nil { ... }
//	defer s.Close()
//
//	// Publish
//	s.Put("demo/hello", []byte("world"))
//
//	// Subscribe
//	sub, _ := s.DeclareSubscriber("demo/**", func(sample zenoh.Sample) {
//	    fmt.Println(sample.KeyExpr, string(sample.Payload))
//	})
//	defer sub.Undeclare()
package zenoh

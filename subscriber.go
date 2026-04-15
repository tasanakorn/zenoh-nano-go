package zenoh

import (
	"log"
	"sync"
)

// Subscriber is a registered subscription.
type Subscriber struct {
	session *Session
	id      uint32
	keyExpr string
	pattern string
	cb      func(Sample)
	ch      chan Sample
	done    chan struct{}

	closeOnce sync.Once
}

// dispatch runs on its own goroutine and invokes cb for each queued sample.
func (s *Subscriber) dispatch() {
	defer close(s.done)
	for sample := range s.ch {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("zenoh: subscriber callback panic: %v", r)
				}
			}()
			s.cb(sample)
		}()
	}
}

// shutdown closes the sample channel (once) and waits for the dispatcher to exit.
func (s *Subscriber) shutdown() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() { close(s.ch) })
	<-s.done
}

// Undeclare removes the subscriber and stops callbacks.
func (s *Subscriber) Undeclare() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.undeclareSubscriber(s.id)
}

// Close is an alias for Undeclare.
func (s *Subscriber) Close() error { return s.Undeclare() }

// KeyExpr returns the subscriber's key expression pattern.
func (s *Subscriber) KeyExpr() string { return s.keyExpr }

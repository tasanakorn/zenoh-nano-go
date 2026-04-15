package zenoh

// Publisher is a thin handle for repeated publications to the same key.
// v0.1.0 does not pre-declare a numeric ID; every Put encodes the key string.
type Publisher struct {
	session *Session
	keyExpr string
}

// DeclarePublisher returns a Publisher bound to keyExpr.
func (s *Session) DeclarePublisher(keyExpr string) (*Publisher, error) {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return nil, err
	}
	return &Publisher{session: s, keyExpr: keyExpr}, nil
}

// Put publishes a value via the Publisher.
func (p *Publisher) Put(payload []byte) error {
	return p.session.Put(p.keyExpr, payload)
}

// Delete publishes a DELETE via the Publisher.
func (p *Publisher) Delete() error {
	return p.session.Delete(p.keyExpr)
}

// Undeclare releases the Publisher. No-op in v0.1.0.
func (p *Publisher) Undeclare() error { return nil }

// KeyExpr returns the publisher's key expression.
func (p *Publisher) KeyExpr() string { return p.keyExpr }

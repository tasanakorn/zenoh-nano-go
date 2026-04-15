package zenoh

import "context"

// Querier is a reusable handle for issuing Get queries against a fixed key expression.
type Querier struct {
	session *Session
	keyExpr string
}

// DeclareQuerier returns a Querier bound to keyExpr.
func (s *Session) DeclareQuerier(keyExpr string) (*Querier, error) {
	if err := ValidateKeyExpr(keyExpr); err != nil {
		return nil, err
	}
	return &Querier{session: s, keyExpr: keyExpr}, nil
}

// Get executes a query with optional parameters (may be empty).
func (q *Querier) Get(ctx context.Context, parameters string) ([]Reply, error) {
	sel := q.keyExpr
	if parameters != "" {
		sel = q.keyExpr + "?" + parameters
	}
	return q.session.Get(ctx, sel)
}

// Undeclare releases the Querier. No-op in v0.1.0.
func (q *Querier) Undeclare() error { return nil }

// KeyExpr returns the querier's key expression.
func (q *Querier) KeyExpr() string { return q.keyExpr }

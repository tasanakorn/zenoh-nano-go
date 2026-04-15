package zenoh

import "strings"

// ValidateKeyExpr performs minimal validation on a key expression.
// Zenoh key expressions must be non-empty, not contain "//", and not start or end with '/'.
func ValidateKeyExpr(ke string) error {
	if ke == "" {
		return ErrInvalidKeyExpr
	}
	if strings.Contains(ke, "//") {
		return ErrInvalidKeyExpr
	}
	if strings.HasPrefix(ke, "/") || strings.HasSuffix(ke, "/") {
		return ErrInvalidKeyExpr
	}
	return nil
}

// SplitSelector splits "keyexpr?params" into (keyexpr, params).
// If no '?' is present, params is empty.
func SplitSelector(sel string) (string, string) {
	i := strings.IndexByte(sel, '?')
	if i < 0 {
		return sel, ""
	}
	return sel[:i], sel[i+1:]
}

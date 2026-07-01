package identity

import "context"

type contextKey struct{}

// WithClaims stores token claims in the context.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

// ClaimsFrom retrieves token claims from the context. Returns nil if absent.
func ClaimsFrom(ctx context.Context) *Claims {
	v, _ := ctx.Value(contextKey{}).(*Claims)
	return v
}

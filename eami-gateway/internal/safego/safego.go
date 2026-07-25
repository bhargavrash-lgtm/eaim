// Package safego provides panic recovery for background goroutines.
//
// eami-gateway runs several long-lived/background goroutines (the approval
// router's LISTEN loop, fire-and-forget token-usage writes, episode
// recording) outside any HTTP request path, so there is no Recoverer
// middleware protecting them — an unrecovered panic in any of them crashes
// the whole process. Guard closes that gap uniformly.
package safego

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Guard runs fn and recovers any panic it raises, logging the goroutine's
// name, the panic value, and a stack trace before returning normally. It
// never re-panics, so a caller invoking Guard from inside a loop can rely on
// the loop continuing to its next iteration after a panic.
func Guard(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered panic in background goroutine",
				"goroutine", name,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()
	fn()
}

// GuardErr is Guard's counterpart for callers whose loop body already
// returns an error (e.g. to drive a retry/reconnect path): it recovers any
// panic the same way Guard does (logged with name/value/stack) and converts
// it into an error instead of only logging it, so existing error-handling
// logic can treat "panicked" the same as any other failure.
func GuardErr(name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered panic in background goroutine",
				"goroutine", name,
				"panic", r,
				"stack", string(debug.Stack()),
			)
			err = fmt.Errorf("%s: recovered panic: %v", name, r)
		}
	}()
	return fn()
}

package alerting

import (
	"log"
	"runtime/debug"
)

// guard runs fn and recovers any panic it raises, logging the goroutine
// name, the panic value, and a stack trace before returning normally. It
// never re-panics — a caller invoking guard from inside a loop can rely on
// the loop continuing to its next iteration after a panic.
//
// The engine is the only background/ticker goroutine in this module, so this
// stays a small unexported helper local to the package rather than a shared
// cross-package utility (see eami-gateway/internal/safego for the sibling
// module's equivalent, used by three call sites there).
func guard(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("alerting: recovered panic in %s: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}

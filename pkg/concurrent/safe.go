package concurrent

import "log"

// SafeGo runs fn in a new goroutine with panic recovery.
// If fn panics, the panic is recovered and logged with the goroutine name.
// Use this for all fire-and-forget goroutines to prevent a single panic
// from crashing the entire process.
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[panic recovery] goroutine %q panicked: %v", name, r)
			}
		}()
		fn()
	}()
}

// SafeGoFunc runs fn(arg) in a new goroutine with panic recovery.
// Useful when you need to pass a captured argument to avoid closure variable issues.
func SafeGoFunc[T any](name string, arg T, fn func(T)) {
	go func(a T) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[panic recovery] goroutine %q panicked: %v", name, r)
			}
		}()
		fn(a)
	}(arg)
}

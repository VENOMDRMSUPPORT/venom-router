// Package models is the catalog domain (01 §3): canonical model identity,
// provider-model aliases, offerings, offering-operations, and the
// certification lifecycle. It is pure: no I/O, no persistence, no clock or
// randomness read from the environment. Every function that needs the
// current time takes it as a parameter (an injected clock) rather than
// calling time.Now() itself, so every test is deterministic. This package
// imports nothing from internal/* — internal/storage implements the
// interfaces this package's future application layer will define, never
// the reverse.
package models

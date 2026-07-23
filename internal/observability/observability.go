// Package observability provides the structured, secret-free logging
// boundary used across the codebase, built on the standard library
// log/slog. Callers attach typed key/value fields (String, Int, Bool,
// Duration, Err) rather than interpolating values into a message string
// or a printf-style surface.
//
// This package deliberately has no "log this whole struct/value"
// convenience (no generic Any/Field-from-interface{} constructor): that
// is exactly the kind of shortcut that later ends up dumping a struct
// containing a credential. Full secret redaction is a separate concern
// landing in P1-SEC-005's sanitize package; this package does not
// implement it and does not need to, as long as callers only pass the
// typed fields this API actually exposes.
package observability

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// Field is a single structured, typed log attribute.
type Field = slog.Attr

// String returns a typed string Field.
func String(key, value string) Field { return slog.String(key, value) }

// Int returns a typed int Field.
func Int(key string, value int) Field { return slog.Int(key, value) }

// Bool returns a typed bool Field.
func Bool(key string, value bool) Field { return slog.Bool(key, value) }

// Duration returns a typed time.Duration Field.
func Duration(key string, value time.Duration) Field { return slog.Duration(key, value) }

// Err returns a typed error Field, keyed "error". Callers are
// responsible for not passing an error whose message embeds a secret;
// this package does not inspect or redact error content.
func Err(err error) Field { return slog.Any("error", err) }

// Logger is the structured logging boundary. It wraps *slog.Logger to
// expose a small, typed API instead of slog's own free-form
// interface{}-args surface.
type Logger struct {
	slog *slog.Logger
}

// New builds a Logger backed by handler. Callers construct the handler
// explicitly (e.g. slog.NewJSONHandler) so the wire format and level
// threshold are a deliberate choice, not a hidden default.
func New(handler slog.Handler) *Logger {
	return &Logger{slog: slog.New(handler)}
}

// Default returns a Logger writing JSON-structured records to os.Stderr
// at Info level and above.
func Default() *Logger {
	return New(slog.NewJSONHandler(os.Stderr, nil))
}

// Debug logs msg at Debug level with the given structured fields.
func (l *Logger) Debug(msg string, fields ...Field) {
	l.slog.LogAttrs(context.Background(), slog.LevelDebug, msg, fields...)
}

// Info logs msg at Info level with the given structured fields.
func (l *Logger) Info(msg string, fields ...Field) {
	l.slog.LogAttrs(context.Background(), slog.LevelInfo, msg, fields...)
}

// Warn logs msg at Warn level with the given structured fields.
func (l *Logger) Warn(msg string, fields ...Field) {
	l.slog.LogAttrs(context.Background(), slog.LevelWarn, msg, fields...)
}

// Error logs msg at Error level with the given structured fields.
func (l *Logger) Error(msg string, fields ...Field) {
	l.slog.LogAttrs(context.Background(), slog.LevelError, msg, fields...)
}

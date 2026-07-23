package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestLogger_EmitsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))

	logger.Info("account enrolled", String("account_id", "acct_123"), Int("attempt", 2))

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal log record: %v (raw: %s)", err, buf.String())
	}

	if record["msg"] != "account enrolled" {
		t.Fatalf("msg = %v, want %q", record["msg"], "account enrolled")
	}
	// The field value must appear as its own structured JSON key, not
	// interpolated into the message string.
	if msg, ok := record["msg"].(string); ok && strings.Contains(msg, "acct_123") {
		t.Fatalf("field value leaked into msg string: %q", msg)
	}
	if got, ok := record["account_id"]; !ok || got != "acct_123" {
		t.Fatalf("account_id = %v (present=%v), want %q as a top-level structured field", got, ok, "acct_123")
	}
	attemptVal, ok := record["attempt"]
	if !ok {
		t.Fatalf("attempt field missing from structured record: %s", buf.String())
	}
	if attemptFloat, ok := attemptVal.(float64); !ok || attemptFloat != 2 {
		t.Fatalf("attempt = %v, want 2", attemptVal)
	}
}

func TestLogger_LevelsAndErrField(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	testErr := errors.New("boom")
	logger.Error("operation failed", Err(testErr))

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal log record: %v (raw: %s)", err, buf.String())
	}
	if record["level"] != "ERROR" {
		t.Fatalf("level = %v, want ERROR", record["level"])
	}
	if record["error"] != "boom" {
		t.Fatalf("error field = %v, want %q as a structured key, not interpolated into msg", record["error"], "boom")
	}
}

func TestLogger_AllLevelsAndFieldTypes(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logger.Debug("debug msg", Bool("flag", true))
	logger.Warn("warn msg", Duration("elapsed", 0))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", len(lines), buf.String())
	}

	var debugRecord map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &debugRecord); err != nil {
		t.Fatalf("unmarshal debug record: %v", err)
	}
	if debugRecord["level"] != "DEBUG" || debugRecord["flag"] != true {
		t.Fatalf("debug record = %v, want level=DEBUG flag=true", debugRecord)
	}

	var warnRecord map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &warnRecord); err != nil {
		t.Fatalf("unmarshal warn record: %v", err)
	}
	if warnRecord["level"] != "WARN" {
		t.Fatalf("warn record level = %v, want WARN", warnRecord["level"])
	}
	if _, ok := warnRecord["elapsed"]; !ok {
		t.Fatalf("elapsed field missing from warn record: %v", warnRecord)
	}
}

func TestDefault_ReturnsUsableLogger(t *testing.T) {
	logger := Default()
	if logger == nil {
		t.Fatal("Default() = nil")
	}
	// Default writes to os.Stderr; this is a smoke check that logging
	// through it does not panic, not an assertion on stderr content.
	logger.Debug("smoke", Bool("ok", true))
}

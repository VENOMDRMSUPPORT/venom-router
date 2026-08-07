//go:build windows

package tray

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWinRunner_StartFailsForMissingExecutable(t *testing.T) {
	r := NewProcessRunner()
	_, err := r.Start(ProcessSpec{Name: "definitely-not-a-real-binary-xyz", Args: nil, Dir: t.TempDir()})
	if err == nil {
		t.Fatal("Start() of a nonexistent executable succeeded, want error")
	}
}

func TestWinRunner_CapturesFailureOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "development.log")
	h, err := NewProcessRunner().Start(ProcessSpec{
		Name:       "cmd",
		Args:       []string{"/c", "echo actionable failure 1>&2 & exit /b 7"},
		Dir:        t.TempDir(),
		OutputPath: logPath,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	err = h.Wait()
	if err == nil || !strings.Contains(err.Error(), "actionable failure") {
		t.Fatalf("Wait() error = %v, want captured actionable detail", err)
	}
	body, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if !bytes.Contains(body, []byte("actionable failure")) {
		t.Fatalf("development log = %q, want child stderr", body)
	}
}

func TestWinRunner_KillTerminatesProcessTree(t *testing.T) {
	r := NewProcessRunner()
	// cmd /c ping loops ~60s; Kill must bring Wait back long before that.
	h, err := r.Start(ProcessSpec{
		Name: "cmd",
		Args: []string{"/c", "ping", "-n", "60", "127.0.0.1"},
		Dir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	done := make(chan struct{})
	go func() { _ = h.Wait(); close(done) }()

	time.Sleep(200 * time.Millisecond) // let cmd spawn its ping child
	if err := h.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	select {
	case <-done:
		// job kill-on-close took the whole tree down
	case <-time.After(5 * time.Second):
		t.Fatal("process still alive 5s after Kill — job object did not kill the tree")
	}
	if err := h.Kill(); err != nil {
		t.Fatalf("second Kill() error = %v, want idempotent nil", err)
	}
}

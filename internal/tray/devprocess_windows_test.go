//go:build windows

package tray

import (
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

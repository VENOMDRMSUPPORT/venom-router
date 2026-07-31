//go:build windows

package main

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestAttachParentConsole_NoOpInConsoleProcess pins the console-build
// safety property: a process that already has a console (every `go test`
// / plain `go build` run) must pass through attachParentConsole
// completely untouched — AttachConsole fails with "already attached" and
// the function returns before looking at any stream. If this ever starts
// mutating os.Stdout/os.Stderr in a console process, redirection and
// normal terminal output in dev builds would break.
func TestAttachParentConsole_NoOpInConsoleProcess(t *testing.T) {
	outBefore, errBefore, inBefore := os.Stdout, os.Stderr, os.Stdin
	attachParentConsole()
	if os.Stdout != outBefore {
		t.Fatal("attachParentConsole replaced os.Stdout in a console process")
	}
	if os.Stderr != errBefore {
		t.Fatal("attachParentConsole replaced os.Stderr in a console process")
	}
	if os.Stdin != inBefore {
		t.Fatal("attachParentConsole replaced os.Stdin in a console process")
	}
}

// TestStdHandleValid_TrueForProvidedStreams pins the redirection
// contract's gate: a stream that was explicitly provided to the process
// (go test pipes stdout/stderr, exactly like `venom version > f.txt`
// provides a file handle) must snapshot as VALID, which is what makes
// attachParentConsole leave it untouched. If this ever returns false for
// a provided stream, the attach path would clobber redirections with
// CONOUT$.
func TestStdHandleValid_TrueForProvidedStreams(t *testing.T) {
	if !stdHandleValid(windows.STD_OUTPUT_HANDLE) {
		t.Fatal("stdHandleValid(STD_OUTPUT_HANDLE) = false for a provided (piped) stdout")
	}
	if !stdHandleValid(windows.STD_ERROR_HANDLE) {
		t.Fatal("stdHandleValid(STD_ERROR_HANDLE) = false for a provided (piped) stderr")
	}
}

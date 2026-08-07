//go:build windows

package tray

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NewProcessRunner returns the Windows dev-process runner: every child is
// placed in its own Job Object configured JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
// so Kill — or the tray process itself dying, which closes the job handle —
// tears down the entire process tree (npm/vite's node children, go run's
// compiled child). No orphaned dev processes, ever.
func NewProcessRunner() ProcessRunner { return winRunner{} }

type winRunner struct{}

// createNoWindow keeps dev children from popping consoles (the tray hides
// its own console; its children must not open new ones).
const createNoWindow = 0x08000000

func (winRunner) Start(spec ProcessSpec) (ProcessHandle, error) {
	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.ExtraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	tail := &tailWriter{limit: 4096}
	var output *os.File
	if spec.OutputPath != "" {
		if err := os.MkdirAll(filepath.Dir(spec.OutputPath), 0o700); err != nil {
			return nil, fmt.Errorf("tray: create dev log directory: %w", err)
		}
		var err error
		output, err = os.OpenFile(spec.OutputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("tray: open dev log: %w", err)
		}
		cmd.Stdout = io.MultiWriter(output, tail)
		cmd.Stderr = io.MultiWriter(output, tail)
	} else {
		cmd.Stdout = tail
		cmd.Stderr = tail
	}

	job, err := newKillOnCloseJob()
	if err != nil {
		if output != nil {
			_ = output.Close()
		}
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		if output != nil {
			_ = output.Close()
		}
		return nil, err
	}
	if err := assignToJob(job, cmd.Process.Pid); err != nil {
		// Containment failed — do not run an uncontained tree.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		if output != nil {
			_ = output.Close()
		}
		return nil, err
	}
	return &winHandle{cmd: cmd, job: job, output: output, tail: tail}, nil
}

type tailWriter struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.limit:]...)
	}
	return len(p), nil
}

func (w *tailWriter) detail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	detail := strings.Join(strings.Fields(string(w.buf)), " ")
	if len(detail) > 240 {
		detail = detail[len(detail)-240:]
	}
	return detail
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignToJob(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.AssignProcessToJobObject(job, h)
}

type winHandle struct {
	cmd      *exec.Cmd
	job      windows.Handle
	output   *os.File
	tail     *tailWriter
	killOnce sync.Once
	killErr  error
}

func (h *winHandle) Wait() error {
	err := h.cmd.Wait()
	if h.output != nil {
		_ = h.output.Close()
	}
	if err != nil {
		if detail := h.tail.detail(); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
	}
	return err
}

// Kill closes the job handle; kill-on-close terminates the whole tree.
func (h *winHandle) Kill() error {
	h.killOnce.Do(func() { h.killErr = windows.CloseHandle(h.job) })
	return h.killErr
}

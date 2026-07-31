//go:build windows

package tray

import (
	"os"
	"os/exec"
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

	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := assignToJob(job, cmd.Process.Pid); err != nil {
		// Containment failed — do not run an uncontained tree.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &winHandle{cmd: cmd, job: job}, nil
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
	killOnce sync.Once
	killErr  error
}

func (h *winHandle) Wait() error { return h.cmd.Wait() }

// Kill closes the job handle; kill-on-close terminates the whole tree.
func (h *winHandle) Kill() error {
	h.killOnce.Do(func() { h.killErr = windows.CloseHandle(h.job) })
	return h.killErr
}

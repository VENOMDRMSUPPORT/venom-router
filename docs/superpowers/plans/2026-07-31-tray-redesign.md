# Tray Redesign (Production/Development Split) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Windows tray match the owner's screenshots 1:1 — new triangle icon, and a two-section menu (Production controls for the in-process server; Development controls supervising `vite` + an isolated dev backend).

**Architecture:** A platform-neutral `DevSupervisor` in `internal/tray` drives two child processes (npm/vite frontend, `go run` backend) through a `ProcessRunner` port; the Windows adapter implements the port with a kill-on-close Job Object so the whole dev tree dies with Stop or with the tray itself. The dev backend is isolated from production via a new `VENOM_DATA_DIR` override in `internal/platform` (separate lock, DB, keyring), and vite's `/api` proxy target becomes env-configurable.

**Tech Stack:** Go (fyne.io/systray, golang.org/x/sys/windows), Vite/TypeScript config, plain-Go ICO generation tool.

**Spec:** `docs/superpowers/specs/2026-07-31-tray-redesign-design.md`

## Global Constraints

- English only in every repo file (code, comments, commits). Chat language never leaks into files.
- forbidigo: no `fmt.Print/Printf/Println`, no `panic`, and `os.Getenv`/`os.LookupEnv` ONLY inside `internal/config` and `internal/platform`. (`fmt.Fprintln(os.Stderr, …)` is allowed; `os.Environ()` when building a child process env is allowed.)
- This task is standalone: do NOT touch the roadmap tracker, CI workflows, `internal/app` semantics, or `Controller`'s bounded-exit machinery.
- Dev ports are fixed by the spec: frontend `127.0.0.1:5173`, backend `127.0.0.1:8082`.
- Windows host: run tests with `go test ./...` (no `-race` locally — no gcc; CI covers it).
- Commit after every task; pushing to `main` happens only after the governor's review at the end.

---

### Task 1: `VENOM_DATA_DIR` override + `DevRoot` in `internal/platform`

**Files:**
- Modify: `internal/platform/platform.go` (EnsureDataDir + new DevRoot)
- Create: `internal/platform/platform_test.go` (shared, no build tag)
- Modify: `internal/app/lock_test.go` (add isolation test)

**Interfaces:**
- Produces: `platform.EnsureDataDir()` honoring `VENOM_DATA_DIR`; `platform.DevRoot() string` returning the `VENOM_DEV_ROOT` env value ("" when unset). Task 3 consumes `DevRoot`; Task 3's backend spec relies on the override behavior.

- [ ] **Step 1: Write the failing tests**

Create `internal/platform/platform_test.go`:

```go
package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDataDir_HonorsVenomDataDirOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "override-data")
	t.Setenv("VENOM_DATA_DIR", override)

	got, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() error = %v", err)
	}
	if got != override {
		t.Fatalf("EnsureDataDir() = %q, want the VENOM_DATA_DIR override %q", got, override)
	}
	info, err := os.Stat(got)
	if err != nil || !info.IsDir() {
		t.Fatalf("override dir was not created: info=%v err=%v", info, err)
	}
}

func TestEnsureDataDir_EmptyOverrideFallsBackToDefault(t *testing.T) {
	t.Setenv("VENOM_DATA_DIR", "")

	def, err := DataDir()
	if err != nil {
		t.Skipf("no OS default data dir in this environment: %v", err)
	}
	got, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() error = %v", err)
	}
	if got != def {
		t.Fatalf("EnsureDataDir() with empty override = %q, want OS default %q", got, def)
	}
}

func TestDevRoot(t *testing.T) {
	t.Setenv("VENOM_DEV_ROOT", "")
	if got := DevRoot(); got != "" {
		t.Fatalf("DevRoot() with unset/empty env = %q, want empty", got)
	}
	want := filepath.Join("C:", "repo")
	t.Setenv("VENOM_DEV_ROOT", want)
	if got := DevRoot(); got != want {
		t.Fatalf("DevRoot() = %q, want %q", got, want)
	}
}
```

Append to `internal/app/lock_test.go`:

```go
func TestAcquireLock_SeparateDataDirsDoNotCollide(t *testing.T) {
	t.Setenv("VENOM_DATA_DIR", filepath.Join(t.TempDir(), "a"))
	l1, err := AcquireLock()
	if err != nil {
		t.Fatalf("first AcquireLock() error = %v", err)
	}
	defer func() { _ = l1.Release() }()

	t.Setenv("VENOM_DATA_DIR", filepath.Join(t.TempDir(), "b"))
	l2, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock() in a second data dir error = %v, want success (dev backend isolation)", err)
	}
	defer func() { _ = l2.Release() }()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/ ./internal/app/ -run 'TestEnsureDataDir_Honors|TestEnsureDataDir_Empty|TestDevRoot|TestAcquireLock_SeparateDataDirs' -v`
Expected: FAIL — `undefined: DevRoot`, and the override test fails because the env var is ignored.

- [ ] **Step 3: Implement**

In `internal/platform/platform.go`, replace `EnsureDataDir` and add `DevRoot`:

```go
// EnsureDataDir resolves the application data directory and creates it, and
// any missing parents, if it does not already exist. Creation is idempotent.
//
// Resolution order: the VENOM_DATA_DIR environment variable when set and
// non-empty (a dev/ops override — the tray's Development section uses it to
// give the supervised dev backend a fully isolated lock/DB/keyring), then the
// OS-specific DataDir default.
func EnsureDataDir() (string, error) {
	dir, ok := os.LookupEnv("VENOM_DATA_DIR")
	if !ok || dir == "" {
		var err error
		dir, err = DataDir()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("platform: create data dir %q: %w", dir, err)
	}
	return dir, nil
}

// DevRoot returns the VENOM_DEV_ROOT override for the tray's Development
// section, or "" when unset or empty. The env read lives here because
// forbidigo confines os.Getenv/os.LookupEnv to internal/config and
// internal/platform.
func DevRoot() string {
	v, _ := os.LookupEnv("VENOM_DEV_ROOT")
	return v
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/platform/ ./internal/app/ -v`
Expected: PASS (all, including the pre-existing lock and data-dir tests).

- [ ] **Step 5: Commit**

```bash
git add internal/platform/platform.go internal/platform/platform_test.go internal/app/lock_test.go
git commit -m "feat(platform): VENOM_DATA_DIR data-dir override and VENOM_DEV_ROOT accessor"
```

---

### Task 2: New tray icon (generator tool + regenerated asset + parse test)

**Files:**
- Create: `tools/genicon/main.go`
- Modify: `internal/tray/assets/venom.ico` (regenerated output)
- Create: `internal/tray/icon_windows_test.go`

**Interfaces:**
- Produces: `internal/tray/assets/venom.ico` — the embedded `trayIcon` bytes (`icon_windows.go` is untouched). No other task depends on this one.

- [ ] **Step 1: Write the failing test**

Create `internal/tray/icon_windows_test.go`:

```go
//go:build windows

package tray

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// wantIconSizes are the square pixel sizes the redesigned icon must carry
// (design 2026-07-31): small tray/taskbar sizes through 64px.
var wantIconSizes = []int{16, 20, 24, 32, 48, 64}

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// TestTrayIcon_IsPNGCompressedICOWithExpectedSizes parses the embedded ICO
// by hand (ICONDIR + ICONDIRENTRY are trivially small structures) and
// asserts entry count, per-entry square dimensions, and that every payload
// is a PNG (the generator writes PNG-compressed entries).
func TestTrayIcon_IsPNGCompressedICOWithExpectedSizes(t *testing.T) {
	if len(trayIcon) < 6 {
		t.Fatalf("embedded icon too small: %d bytes", len(trayIcon))
	}
	if rt := binary.LittleEndian.Uint16(trayIcon[2:4]); rt != 1 {
		t.Fatalf("ICONDIR type = %d, want 1 (icon)", rt)
	}
	count := int(binary.LittleEndian.Uint16(trayIcon[4:6]))
	if count != len(wantIconSizes) {
		t.Fatalf("icon entry count = %d, want %d", count, len(wantIconSizes))
	}
	for i := 0; i < count; i++ {
		e := trayIcon[6+16*i : 6+16*i+16]
		w, h := int(e[0]), int(e[1])
		if w != wantIconSizes[i] || h != wantIconSizes[i] {
			t.Errorf("entry %d = %dx%d, want %dx%d", i, w, h, wantIconSizes[i], wantIconSizes[i])
		}
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		off := int(binary.LittleEndian.Uint32(e[12:16]))
		if off+size > len(trayIcon) {
			t.Fatalf("entry %d payload [%d:%d] out of bounds (%d)", i, off, off+size, len(trayIcon))
		}
		if !bytes.HasPrefix(trayIcon[off:off+size], pngMagic) {
			t.Errorf("entry %d payload is not PNG-compressed", i)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tray/ -run TestTrayIcon_ -v`
Expected: FAIL — the current `venom.ico` has different entries/encoding.

- [ ] **Step 3: Write the generator**

Create `tools/genicon/main.go`:

```go
// Command genicon regenerates internal/tray/assets/venom.ico: an
// upward-pointing green triangle on a dark rounded square (the owner's
// approved tray icon, design 2026-07-31). Deterministic pure-Go rendering
// with 4x4 supersampled coverage antialiasing; entries are PNG-compressed.
//
// Usage (from the repo root):
//
//	go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

var sizes = []int{16, 20, 24, 32, 48, 64}

var (
	bg  = color.NRGBA{R: 0x1F, G: 0x21, B: 0x24, A: 0xFF} // dark square
	tri = color.NRGBA{R: 0x2E, G: 0xE6, B: 0xA8, A: 0xFF} // green triangle
)

func main() {
	out := "internal/tray/assets/venom.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "genicon:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	var pngs [][]byte
	for _, n := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(n)); err != nil {
			return err
		}
		pngs = append(pngs, buf.Bytes())
	}
	return os.WriteFile(out, encodeICO(pngs), 0o644)
}

// render draws one n×n frame. Geometry (fractions of n): rounded-square
// corner radius 0.18; triangle apex (0.50, 0.26), base (0.26, 0.72) to
// (0.74, 0.72) — matching the approved screenshot's proportions.
func render(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	fn := float64(n)
	ax, ay := 0.50*fn, 0.26*fn
	bx, by := 0.26*fn, 0.72*fn
	cx, cy := 0.74*fn, 0.72*fn
	r := 0.18 * fn
	const ss = 4 // supersamples per axis
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var sqHits, triHits int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if inRoundedSquare(px, py, fn, r) {
						sqHits++
						if inTriangle(px, py, ax, ay, bx, by, cx, cy) {
							triHits++
						}
					}
				}
			}
			img.SetNRGBA(x, y, blend(sqHits, triHits, ss*ss))
		}
	}
	return img
}

// blend composes transparent -> background -> triangle by sample coverage.
func blend(sqHits, triHits, total int) color.NRGBA {
	if sqHits == 0 {
		return color.NRGBA{}
	}
	fSq := float64(sqHits) / float64(total)
	fTri := float64(triHits) / float64(total)
	mix := func(b, t uint8) uint8 {
		bgPart := (fSq - fTri) * float64(b)
		triPart := fTri * float64(t)
		return uint8((bgPart + triPart) / fSq)
	}
	return color.NRGBA{
		R: mix(bg.R, tri.R),
		G: mix(bg.G, tri.G),
		B: mix(bg.B, tri.B),
		A: uint8(fSq * 255),
	}
}

func inRoundedSquare(px, py, n, r float64) bool {
	if px < 0 || py < 0 || px > n || py > n {
		return false
	}
	// Inside the cross formed by the two inset rectangles?
	if (px >= r && px <= n-r) || (py >= r && py <= n-r) {
		return true
	}
	// Corner regions: within radius of the nearest corner circle center.
	ccx, ccy := r, r
	if px > n-r {
		ccx = n - r
	}
	if py > n-r {
		ccy = n - r
	}
	dx, dy := px-ccx, py-ccy
	return dx*dx+dy*dy <= r*r
}

func inTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	side := func(x1, y1, x2, y2 float64) float64 {
		return (x2-x1)*(py-y1) - (y2-y1)*(px-x1)
	}
	d1 := side(ax, ay, bx, by)
	d2 := side(bx, by, cx, cy)
	d3 := side(cx, cy, ax, ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

// encodeICO wraps PNG blobs in an ICO container: ICONDIR (6 bytes) +
// one 16-byte ICONDIRENTRY per image + concatenated payloads.
func encodeICO(pngs [][]byte) []byte {
	var buf bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, uint16(0)) // reserved
	_ = binary.Write(&buf, le, uint16(1)) // type: icon
	_ = binary.Write(&buf, le, uint16(len(pngs)))
	offset := 6 + 16*len(pngs)
	for i, p := range pngs {
		n := sizes[i]
		buf.WriteByte(byte(n)) // width (256 would be 0; we stay below)
		buf.WriteByte(byte(n)) // height
		buf.WriteByte(0)       // palette colors
		buf.WriteByte(0)       // reserved
		_ = binary.Write(&buf, le, uint16(1))  // color planes
		_ = binary.Write(&buf, le, uint16(32)) // bits per pixel
		_ = binary.Write(&buf, le, uint32(len(p)))
		_ = binary.Write(&buf, le, uint32(offset))
		offset += len(p)
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}
```

- [ ] **Step 4: Regenerate the asset and verify the test passes**

Run (from repo root):
```bash
go run ./tools/genicon
go test ./internal/tray/ -run TestTrayIcon_ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/genicon/main.go internal/tray/assets/venom.ico internal/tray/icon_windows_test.go
git commit -m "feat(tray): regenerate tray icon — green triangle on dark rounded square"
```

---

### Task 3: `DevSupervisor` core (platform-neutral, fully tested)

**Files:**
- Create: `internal/tray/devsupervisor.go`
- Create: `internal/tray/devsupervisor_test.go`

**Interfaces:**
- Consumes: `platform.DevRoot()` (Task 1).
- Produces (Tasks 4/6 rely on these exact names):
  - `type ProcessSpec struct { Dir, Name string; Args []string; ExtraEnv []string }`
  - `type ProcessHandle interface { Wait() error; Kill() error }`
  - `type ProcessRunner interface { Start(spec ProcessSpec) (ProcessHandle, error) }`
  - `type HealthProbe func(ctx context.Context, url string) bool`; `DefaultHealthProbe`
  - `type DevComponentState int` with `DevStopped/DevStarting/DevRunning/DevError`
  - `type DevStatusView struct { Overall, Frontend, Backend DevComponentState }`
  - `type DevSupervisorOptions struct { Root, DataDir string; Runner ProcessRunner; Probe HealthProbe; Logger *observability.Logger }`
  - `NewDevSupervisor(opts DevSupervisorOptions) *DevSupervisor` with methods `Available() bool`, `Start()`, `Stop()`, `Restart()`, `Refresh(ctx)`, `Status() DevStatusView`, `StatusLine() string`, `DashboardURL() string`
  - `ResolveDevRoot() string`

- [ ] **Step 1: Write the failing tests**

Create `internal/tray/devsupervisor_test.go`:

```go
package tray

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHandle is a controllable ProcessHandle: Wait blocks until exit is
// signalled (by the test or by Kill).
type fakeHandle struct {
	exited   chan error
	killOnce sync.Once
	killed   bool
}

func newFakeHandle() *fakeHandle { return &fakeHandle{exited: make(chan error, 1)} }

func (h *fakeHandle) Wait() error { return <-h.exited }

func (h *fakeHandle) Kill() error {
	h.killOnce.Do(func() {
		h.killed = true
		h.exited <- errors.New("killed")
	})
	return nil
}

// exit simulates the child dying on its own.
func (h *fakeHandle) exit(err error) { h.exited <- err }

type fakeRunner struct {
	mu      sync.Mutex
	specs   []ProcessSpec
	handles []*fakeHandle
	// failFor makes Start fail for specs whose Name matches.
	failFor map[string]error
}

func (r *fakeRunner) Start(spec ProcessSpec) (ProcessHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.failFor[spec.Name]; ok {
		return nil, err
	}
	h := newFakeHandle()
	r.specs = append(r.specs, spec)
	r.handles = append(r.handles, h)
	return h, nil
}

func (r *fakeRunner) spawned() []ProcessSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ProcessSpec(nil), r.specs...)
}

func (r *fakeRunner) handle(i int) *fakeHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[i]
}

func probeAlways(ok bool) HealthProbe {
	return func(context.Context, string) bool { return ok }
}

func newTestSupervisor(t *testing.T, runner ProcessRunner, probe HealthProbe) *DevSupervisor {
	t.Helper()
	return NewDevSupervisor(DevSupervisorOptions{
		Root:    filepath.Join("C:", "repo"),
		DataDir: filepath.Join("C:", "data"),
		Runner:  runner,
		Probe:   probe,
	})
}

// eventually polls cond for up to 2s (the watch goroutine is asynchronous).
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestDevSupervisor_StartSpawnsBothComponentsWithApprovedSpecs(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	specs := r.spawned()
	if len(specs) != 2 {
		t.Fatalf("spawned %d processes, want 2 (frontend+backend)", len(specs))
	}
	fe, be := specs[0], specs[1]

	if fe.Dir != filepath.Join("C:", "repo", "dashboard") {
		t.Errorf("frontend dir = %q", fe.Dir)
	}
	if fe.Name != "cmd" {
		t.Errorf("frontend command = %q, want cmd (npm runs through cmd /c on Windows)", fe.Name)
	}
	wantArgs := []string{"/c", "npm", "run", "dev", "--", "--port", "5173", "--strictPort"}
	if strings.Join(fe.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("frontend args = %v, want %v", fe.Args, wantArgs)
	}
	if len(fe.ExtraEnv) != 1 || fe.ExtraEnv[0] != "VENOM_DEV_API_TARGET=http://127.0.0.1:8082" {
		t.Errorf("frontend env = %v, want the dev API proxy target", fe.ExtraEnv)
	}

	if be.Dir != filepath.Join("C:", "repo") {
		t.Errorf("backend dir = %q", be.Dir)
	}
	if be.Name != "go" {
		t.Errorf("backend command = %q, want go", be.Name)
	}
	wantBE := []string{"run", "./cmd/venom", "serve", "-bind", "127.0.0.1:8082"}
	if strings.Join(be.Args, " ") != strings.Join(wantBE, " ") {
		t.Errorf("backend args = %v, want %v", be.Args, wantBE)
	}
	wantEnv := "VENOM_DATA_DIR=" + filepath.Join("C:", "data", "dev")
	if len(be.ExtraEnv) != 1 || be.ExtraEnv[0] != wantEnv {
		t.Errorf("backend env = %v, want [%q] (isolated lock/DB)", be.ExtraEnv, wantEnv)
	}

	v := s.Status()
	if v.Frontend != DevStarting || v.Backend != DevStarting || v.Overall != DevStarting {
		t.Errorf("after Start: %+v, want all Starting", v)
	}
}

func TestDevSupervisor_RefreshPromotesStartingToRunning(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))
	s.Start()

	s.Refresh(context.Background())

	v := s.Status()
	if v.Frontend != DevRunning || v.Backend != DevRunning || v.Overall != DevRunning {
		t.Errorf("after healthy Refresh: %+v, want all Running", v)
	}
}

func TestDevSupervisor_RefreshLeavesStartingWhileUnhealthy(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()

	s.Refresh(context.Background())

	if v := s.Status(); v.Overall != DevStarting {
		t.Errorf("after unhealthy Refresh: %+v, want Starting", v)
	}
}

func TestDevSupervisor_SpawnFailureMarksOnlyThatComponentError(t *testing.T) {
	r := &fakeRunner{failFor: map[string]error{"go": errors.New("no toolchain")}}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	v := s.Status()
	if v.Backend != DevError {
		t.Errorf("backend = %v, want Error on spawn failure", v.Backend)
	}
	if v.Frontend != DevStarting {
		t.Errorf("frontend = %v, want Starting (unaffected)", v.Frontend)
	}
	if v.Overall != DevError {
		t.Errorf("overall = %v, want Error", v.Overall)
	}
}

func TestDevSupervisor_UnexpectedExitMarksError(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()

	r.handle(0).exit(nil) // frontend dies on its own — even a clean exit is unexpected

	eventually(t, func() bool { return s.Status().Frontend == DevError },
		"frontend never reached Error after its process exited")
}

func TestDevSupervisor_StopKillsBothAndStaysStopped(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))
	s.Start()
	s.Refresh(context.Background())

	s.Stop()

	if !r.handle(0).killed || !r.handle(1).killed {
		t.Fatal("Stop did not kill both process handles")
	}
	v := s.Status()
	if v.Frontend != DevStopped || v.Backend != DevStopped || v.Overall != DevStopped {
		t.Errorf("after Stop: %+v, want all Stopped", v)
	}
	// The kill-induced Wait return must NOT flip Stopped to Error (generation
	// guard) — give the watcher goroutines a moment to run.
	time.Sleep(50 * time.Millisecond)
	if v := s.Status(); v.Frontend != DevStopped || v.Backend != DevStopped {
		t.Errorf("watcher clobbered deliberate Stop: %+v", v)
	}
}

func TestDevSupervisor_RestartAfterErrorSpawnsFreshProcesses(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()
	r.handle(1).exit(errors.New("crash"))
	eventually(t, func() bool { return s.Status().Backend == DevError },
		"backend never reached Error")

	s.Restart()

	if got := len(r.spawned()); got != 4 {
		t.Fatalf("total spawns = %d, want 4 (2 initial + 2 restart)", got)
	}
	if v := s.Status(); v.Overall != DevStarting {
		t.Errorf("after Restart: %+v, want Starting", v)
	}
}

func TestDevSupervisor_UnavailableWhenNoRoot(t *testing.T) {
	r := &fakeRunner{}
	s := NewDevSupervisor(DevSupervisorOptions{Root: "", DataDir: "x", Runner: r, Probe: probeAlways(true)})

	if s.Available() {
		t.Fatal("Available() = true with no root")
	}
	s.Start()
	if len(r.spawned()) != 0 {
		t.Fatal("Start spawned processes despite no dev root")
	}
	if got := s.StatusLine(); got != "Dev Status: unavailable" {
		t.Errorf("StatusLine() = %q", got)
	}
}

func TestDevSupervisor_StatusLineFormats(t *testing.T) {
	cases := []struct {
		f, b DevComponentState
		want string
	}{
		{DevStopped, DevStopped, "Dev Status: Stopped · Frontend stopped · Backend stopped"},
		{DevStarting, DevRunning, "Dev Status: Starting · Frontend starting · Backend running"},
		{DevRunning, DevRunning, "Dev Status: Running · Frontend running · Backend running"},
		{DevError, DevRunning, "Dev Status: Error · Frontend error · Backend running"},
	}
	for _, tc := range cases {
		v := DevStatusView{Overall: overallDevState(tc.f, tc.b), Frontend: tc.f, Backend: tc.b}
		if got := v.statusLine(); got != tc.want {
			t.Errorf("statusLine(%v,%v) = %q, want %q", tc.f, tc.b, got, tc.want)
		}
	}
}

func TestOverallDevState(t *testing.T) {
	cases := []struct {
		f, b, want DevComponentState
	}{
		{DevStopped, DevStopped, DevStopped},
		{DevStarting, DevStopped, DevStarting},
		{DevStarting, DevStarting, DevStarting},
		{DevRunning, DevStarting, DevStarting},
		{DevRunning, DevRunning, DevRunning},
		{DevRunning, DevStopped, DevStarting},
		{DevError, DevRunning, DevError},
		{DevError, DevStarting, DevError},
	}
	for _, tc := range cases {
		if got := overallDevState(tc.f, tc.b); got != tc.want {
			t.Errorf("overallDevState(%v,%v) = %v, want %v", tc.f, tc.b, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tray/ -run TestDevSupervisor -v`
Expected: FAIL — `undefined: DevSupervisor` etc.

- [ ] **Step 3: Implement**

Create `internal/tray/devsupervisor.go`:

```go
package tray

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// Development-environment constants (design 2026-07-31). The dev section is a
// Windows desktop affordance; the frontend spec deliberately runs npm through
// cmd /c. Ports are fixed by the approved design.
const (
	devFrontendURL   = "http://127.0.0.1:5173/"
	devBackendBind   = "127.0.0.1:8082"
	devBackendHealth = "http://" + devBackendBind + "/health"
	devAPITarget     = "VENOM_DEV_API_TARGET=http://" + devBackendBind
)

// DevComponentState is one dev child's coarse state.
type DevComponentState int

const (
	DevStopped DevComponentState = iota
	DevStarting
	DevRunning
	DevError
)

// title is the capitalized overall form ("Stopped"); label the lowercase
// per-component form ("stopped") — both exactly as the menu renders them.
func (s DevComponentState) title() string {
	switch s {
	case DevStarting:
		return "Starting"
	case DevRunning:
		return "Running"
	case DevError:
		return "Error"
	default:
		return "Stopped"
	}
}

func (s DevComponentState) label() string {
	switch s {
	case DevStarting:
		return "starting"
	case DevRunning:
		return "running"
	case DevError:
		return "error"
	default:
		return "stopped"
	}
}

// ProcessSpec describes one dev child process.
type ProcessSpec struct {
	Dir      string
	Name     string
	Args     []string
	ExtraEnv []string // KEY=VALUE entries appended to the parent env
}

// ProcessHandle is a started child as the supervisor sees it.
type ProcessHandle interface {
	// Wait blocks until the process exits.
	Wait() error
	// Kill terminates the whole process tree. Idempotent.
	Kill() error
}

// ProcessRunner spawns dev child processes (Windows: inside a kill-on-close
// Job Object; see devprocess_windows.go).
type ProcessRunner interface {
	Start(spec ProcessSpec) (ProcessHandle, error)
}

// HealthProbe reports whether url currently answers HTTP at all.
type HealthProbe func(ctx context.Context, url string) bool

// DefaultHealthProbe: any HTTP response (any status) proves the listener is
// up — vite and the dev backend both answer GET on their probe URLs.
func DefaultHealthProbe(ctx context.Context, url string) bool {
	cctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// DevStatusView is an immutable snapshot for the UI.
type DevStatusView struct {
	Overall  DevComponentState
	Frontend DevComponentState
	Backend  DevComponentState
}

// statusLine renders the menu's dev info line, e.g.
// "Dev Status: Starting · Frontend starting · Backend running".
func (v DevStatusView) statusLine() string {
	return "Dev Status: " + v.Overall.title() +
		" · Frontend " + v.Frontend.label() +
		" · Backend " + v.Backend.label()
}

// overallDevState aggregates: Error dominates, then Starting (any component
// not yet Running while another is up also reads Starting), then Running
// (both), else Stopped.
func overallDevState(f, b DevComponentState) DevComponentState {
	switch {
	case f == DevError || b == DevError:
		return DevError
	case f == DevRunning && b == DevRunning:
		return DevRunning
	case f == DevStarting || b == DevStarting || f == DevRunning || b == DevRunning:
		return DevStarting
	default:
		return DevStopped
	}
}

// DevSupervisorOptions configures NewDevSupervisor.
type DevSupervisorOptions struct {
	// Root is the repo root ("" = Development section unavailable).
	Root string
	// DataDir is the PRODUCTION data dir; the dev backend gets <DataDir>/dev.
	DataDir string
	Runner  ProcessRunner
	Probe   HealthProbe
	Logger  *observability.Logger
}

type devComponent struct {
	state  DevComponentState
	handle ProcessHandle
	// gen invalidates stale watcher goroutines and in-flight starts: Stop
	// bumps it, so a Wait() return from a deliberately killed process can
	// never flip Stopped to Error.
	gen int
}

// DevSupervisor drives the two dev children (vite frontend, go backend)
// through ProcessRunner. Platform-neutral; no syscalls, no os/exec.
type DevSupervisor struct {
	root    string
	dataDir string
	runner  ProcessRunner
	probe   HealthProbe
	log     *observability.Logger

	mu       sync.Mutex
	frontend devComponent
	backend  devComponent

	lifecycleMu sync.Mutex
}

// NewDevSupervisor builds a DevSupervisor, filling defaults.
func NewDevSupervisor(opts DevSupervisorOptions) *DevSupervisor {
	s := &DevSupervisor{
		root:    opts.Root,
		dataDir: opts.DataDir,
		runner:  opts.Runner,
		probe:   opts.Probe,
		log:     opts.Logger,
	}
	if s.log == nil {
		s.log = observability.Default()
	}
	if s.probe == nil {
		s.probe = DefaultHealthProbe
	}
	return s
}

// ResolveDevRoot returns the repo root the Development section controls:
// platform.DevRoot() when set, else the current working directory when it
// holds both go.mod and dashboard/package.json, else "" (unavailable).
func ResolveDevRoot() string {
	if root := platform.DevRoot(); root != "" {
		return root
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if fileExists(filepath.Join(wd, "go.mod")) && fileExists(filepath.Join(wd, "dashboard", "package.json")) {
		return wd
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Available reports whether a dev repo root was resolved.
func (s *DevSupervisor) Available() bool { return s.root != "" }

// DashboardURL is the dev frontend (vite) URL.
func (s *DevSupervisor) DashboardURL() string { return devFrontendURL }

// Status returns the current snapshot.
func (s *DevSupervisor) Status() DevStatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DevStatusView{
		Overall:  overallDevState(s.frontend.state, s.backend.state),
		Frontend: s.frontend.state,
		Backend:  s.backend.state,
	}
}

// StatusLine renders the menu info line, including the unavailable form.
func (s *DevSupervisor) StatusLine() string {
	if !s.Available() {
		return "Dev Status: unavailable"
	}
	return s.Status().statusLine()
}

func (s *DevSupervisor) frontendSpec() ProcessSpec {
	return ProcessSpec{
		Dir:      filepath.Join(s.root, "dashboard"),
		Name:     "cmd",
		Args:     []string{"/c", "npm", "run", "dev", "--", "--port", "5173", "--strictPort"},
		ExtraEnv: []string{devAPITarget},
	}
}

func (s *DevSupervisor) backendSpec() ProcessSpec {
	return ProcessSpec{
		Dir:      s.root,
		Name:     "go",
		Args:     []string{"run", "./cmd/venom", "serve", "-bind", devBackendBind},
		ExtraEnv: []string{"VENOM_DATA_DIR=" + filepath.Join(s.dataDir, "dev")},
	}
}

// Start spawns any component that is not already Starting/Running. No-op
// when the dev root is unavailable.
func (s *DevSupervisor) Start() {
	if !s.Available() {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.startComponent(&s.frontend, "frontend", s.frontendSpec())
	s.startComponent(&s.backend, "backend", s.backendSpec())
}

// Stop kills both components and marks them Stopped. Deliberate: the bumped
// generation makes the watcher ignore the kill-induced Wait return.
func (s *DevSupervisor) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopComponent(&s.frontend)
	s.stopComponent(&s.backend)
}

// Restart is Stop then Start.
func (s *DevSupervisor) Restart() {
	s.Stop()
	s.Start()
}

// Refresh promotes Starting components to Running once their health probe
// answers (called from the UI ticker).
func (s *DevSupervisor) Refresh(ctx context.Context) {
	s.refreshComponent(ctx, &s.frontend, devFrontendURL)
	s.refreshComponent(ctx, &s.backend, devBackendHealth)
}

func (s *DevSupervisor) startComponent(c *devComponent, name string, spec ProcessSpec) {
	s.mu.Lock()
	if c.state == DevStarting || c.state == DevRunning {
		s.mu.Unlock()
		return
	}
	c.gen++
	gen := c.gen
	s.mu.Unlock()

	h, err := s.runner.Start(spec)

	s.mu.Lock()
	defer s.mu.Unlock()
	if c.gen != gen {
		// A Stop raced this start; don't leak the late arrival.
		if err == nil {
			_ = h.Kill()
		}
		return
	}
	if err != nil {
		c.state = DevError
		s.log.Error("tray: dev component failed to start",
			observability.String("component", name),
			observability.String("err", err.Error()))
		return
	}
	c.state = DevStarting
	c.handle = h
	go s.watch(c, name, h, gen)
}

// watch turns an unexpected child exit — clean or not — into Error: dev
// servers must not exit on their own, so a self-exit is always a defect the
// owner should see (a deliberate Stop bumps gen first and is ignored here).
func (s *DevSupervisor) watch(c *devComponent, name string, h ProcessHandle, gen int) {
	err := h.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.gen != gen {
		return
	}
	c.state = DevError
	c.handle = nil
	detail := "exited"
	if err != nil {
		detail = err.Error()
	}
	s.log.Error("tray: dev component exited unexpectedly",
		observability.String("component", name),
		observability.String("err", detail))
}

func (s *DevSupervisor) stopComponent(c *devComponent) {
	s.mu.Lock()
	c.gen++
	h := c.handle
	c.handle = nil
	c.state = DevStopped
	s.mu.Unlock()
	if h != nil {
		_ = h.Kill()
	}
}

func (s *DevSupervisor) refreshComponent(ctx context.Context, c *devComponent, url string) {
	s.mu.Lock()
	starting := c.state == DevStarting
	s.mu.Unlock()
	if !starting || !s.probe(ctx, url) {
		return
	}
	s.mu.Lock()
	if c.state == DevStarting {
		c.state = DevRunning
	}
	s.mu.Unlock()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tray/ -v`
Expected: PASS (new suite + all pre-existing tray tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tray/devsupervisor.go internal/tray/devsupervisor_test.go
git commit -m "feat(tray): DevSupervisor core — dev frontend/backend state machines over a ProcessRunner port"
```

---

### Task 4: Windows `ProcessRunner` (Job Object, kill-on-close) + non-Windows stub

**Files:**
- Create: `internal/tray/devprocess_windows.go`
- Create: `internal/tray/devprocess_other.go`
- Create: `internal/tray/devprocess_windows_test.go`

**Interfaces:**
- Consumes: `ProcessSpec`, `ProcessHandle`, `ProcessRunner` (Task 3).
- Produces: `NewProcessRunner() ProcessRunner` on BOTH platforms (Task 6's cli wiring calls it unconditionally).

- [ ] **Step 1: Write the failing test**

Create `internal/tray/devprocess_windows_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tray/ -run TestWinRunner_ -v`
Expected: FAIL — `undefined: NewProcessRunner`.

- [ ] **Step 3: Implement**

Create `internal/tray/devprocess_windows.go`:

```go
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
```

Create `internal/tray/devprocess_other.go`:

```go
//go:build !windows

package tray

import "errors"

// NewProcessRunner on non-Windows refuses to spawn: the Development section
// is a Windows desktop affordance (the tray UI itself only exists there).
func NewProcessRunner() ProcessRunner { return unsupportedRunner{} }

type unsupportedRunner struct{}

func (unsupportedRunner) Start(ProcessSpec) (ProcessHandle, error) {
	return nil, errors.New("tray: dev process supervision is unsupported on this platform")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tray/ -run TestWinRunner_ -v`
Expected: PASS (the tree-kill test takes ~1s, not 60s).

- [ ] **Step 5: Commit**

```bash
git add internal/tray/devprocess_windows.go internal/tray/devprocess_other.go internal/tray/devprocess_windows_test.go
git commit -m "feat(tray): Windows dev ProcessRunner with kill-on-close Job Object containment"
```

---

### Task 5: Vite proxy target from env

**Files:**
- Modify: `dashboard/vite.config.ts`

**Interfaces:**
- Consumes: `VENOM_DEV_API_TARGET` env value (set by Task 3's frontend spec).
- Produces: dev proxy that follows the tray-supervised backend.

- [ ] **Step 1: Edit the config**

Replace the contents of `dashboard/vite.config.ts` with:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev mode: npm run dev proxies /api → the control plane. The target defaults
// to the standalone bind (127.0.0.1:8081); the tray's Development section
// overrides it via VENOM_DEV_API_TARGET to point at the supervised dev
// backend (127.0.0.1:8082) so dev traffic never touches production state.
const apiTarget = process.env.VENOM_DEV_API_TARGET ?? "http://127.0.0.1:8081";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
    },
  },
});
```

- [ ] **Step 2: Verify typecheck and build stay green**

Run (in `dashboard/`): `npm run typecheck` then `npm run build`
Expected: both succeed. If TypeScript cannot see `process`, add `import process from "node:process";` at the top instead of any `@types/node` dependency change.

- [ ] **Step 3: Commit**

```bash
git add dashboard/vite.config.ts
git commit -m "feat(dashboard): vite /api proxy target configurable via VENOM_DEV_API_TARGET"
```

---

### Task 6: Menu rebuild, enablement logic, and wiring

**Files:**
- Create: `internal/tray/menu.go` (platform-neutral pure logic)
- Create: `internal/tray/menu_test.go`
- Modify: `internal/tray/tray_windows.go` (menu layout + loop; `statusTitle` moves out)
- Modify: `internal/tray/tray_other.go` (signature)
- Modify: `internal/tray/tray.go` (add `Controller.OpenURL`)
- Modify: `internal/cli/cli.go` (construct the supervisor, pass it in)

**Interfaces:**
- Consumes: `DevSupervisor` API (Task 3), `NewProcessRunner` (Task 4), `platform.DevRoot` via `ResolveDevRoot` (Tasks 1+3).
- Produces:
  - `RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller, dev *DevSupervisor) error` (both platforms)
  - `func (c *Controller) OpenURL(url string)`
  - `type menuEnablement struct { Open, Start, Stop, Restart bool }`
  - `func prodEnablement(s State) menuEnablement`
  - `func devEnablement(available bool, v DevStatusView) menuEnablement`
  - `func statusTitle(s StatusView) string` (moved to menu.go, new copy: `Status: Running|Stopped|Error`)

- [ ] **Step 1: Write the failing tests**

Create `internal/tray/menu_test.go`:

```go
package tray

import "testing"

func TestStatusTitle_MatchesApprovedCopy(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateRunning, "Status: Running"},
		{StateStopped, "Status: Stopped"},
		{StateError, "Status: Error"},
	}
	for _, tc := range cases {
		if got := statusTitle(StatusView{State: tc.state}); got != tc.want {
			t.Errorf("statusTitle(%v) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestProdEnablement(t *testing.T) {
	cases := []struct {
		state State
		want  menuEnablement
	}{
		{StateRunning, menuEnablement{Open: true, Start: false, Stop: true, Restart: true}},
		{StateStopped, menuEnablement{Open: false, Start: true, Stop: false, Restart: false}},
		{StateError, menuEnablement{Open: false, Start: true, Stop: false, Restart: false}},
	}
	for _, tc := range cases {
		if got := prodEnablement(tc.state); got != tc.want {
			t.Errorf("prodEnablement(%v) = %+v, want %+v", tc.state, got, tc.want)
		}
	}
}

func TestDevEnablement(t *testing.T) {
	if got := devEnablement(false, DevStatusView{}); got != (menuEnablement{}) {
		t.Errorf("unavailable dev section must disable everything, got %+v", got)
	}
	cases := []struct {
		name string
		v    DevStatusView
		want menuEnablement
	}{
		{"stopped", DevStatusView{Overall: DevStopped, Frontend: DevStopped, Backend: DevStopped},
			menuEnablement{Open: false, Start: true, Stop: false, Restart: false}},
		{"starting", DevStatusView{Overall: DevStarting, Frontend: DevStarting, Backend: DevStarting},
			menuEnablement{Open: false, Start: false, Stop: true, Restart: true}},
		{"frontend up first", DevStatusView{Overall: DevStarting, Frontend: DevRunning, Backend: DevStarting},
			menuEnablement{Open: true, Start: false, Stop: true, Restart: true}},
		{"running", DevStatusView{Overall: DevRunning, Frontend: DevRunning, Backend: DevRunning},
			menuEnablement{Open: true, Start: false, Stop: true, Restart: true}},
		{"error", DevStatusView{Overall: DevError, Frontend: DevError, Backend: DevRunning},
			menuEnablement{Open: false, Start: true, Stop: true, Restart: true}},
	}
	for _, tc := range cases {
		if got := devEnablement(true, tc.v); got != tc.want {
			t.Errorf("%s: devEnablement = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tray/ -run 'TestStatusTitle|TestProdEnablement|TestDevEnablement' -v`
Expected: FAIL — `undefined: menuEnablement` (and `statusTitle` is windows-only today, so on this Windows host it fails on copy mismatch instead; both count).

- [ ] **Step 3: Implement the pure logic**

Create `internal/tray/menu.go`:

```go
package tray

// This file is the menu's pure decision logic — extracted from the Windows
// adapter so the enablement rules and label copy are testable on any OS.

// statusTitle renders the production info line exactly as the approved
// screenshot shows it ("Status: Error"): coarse state only, no detail.
func statusTitle(s StatusView) string {
	switch s.State {
	case StateRunning:
		return "Status: Running"
	case StateError:
		return "Status: Error"
	default:
		return "Status: Stopped"
	}
}

// menuEnablement says which of a section's four actionable items are enabled.
type menuEnablement struct {
	Open    bool
	Start   bool
	Stop    bool
	Restart bool
}

// prodEnablement: the dashboard only opens on a running server; Start only
// offered when not running; Stop/Restart only when running. This produces
// the greyed items visible in the approved screenshot.
func prodEnablement(s State) menuEnablement {
	if s == StateRunning {
		return menuEnablement{Open: true, Stop: true, Restart: true}
	}
	return menuEnablement{Start: true}
}

// devEnablement: everything disabled when no dev repo was found. Otherwise
// Start is offered from Stopped/Error; Stop/Restart whenever anything is
// live or wedged; Open as soon as the frontend (vite) itself is up.
func devEnablement(available bool, v DevStatusView) menuEnablement {
	if !available {
		return menuEnablement{}
	}
	anyActive := v.Frontend != DevStopped || v.Backend != DevStopped
	return menuEnablement{
		Open:    v.Frontend == DevRunning,
		Start:   v.Overall == DevStopped || v.Overall == DevError,
		Stop:    anyActive,
		Restart: anyActive,
	}
}
```

Add to `internal/tray/tray.go` (next to `OpenDashboard`) and refactor `OpenDashboard` to reuse it:

```go
// OpenURL opens an arbitrary URL with the OS opener (used by the menu's
// dashboard entries).
func (c *Controller) OpenURL(url string) {
	if err := c.op.Open(url); err != nil {
		c.log.Error("tray: open url failed", observability.String("err", err.Error()))
	}
}

// OpenDashboard opens the production dashboard URL in the default browser.
func (c *Controller) OpenDashboard() { c.OpenURL(c.lc.DashboardURL()) }
```

- [ ] **Step 4: Rebuild the Windows menu**

In `internal/tray/tray_windows.go`:

1. Change the signature to `func RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller, dev *DevSupervisor) error`.
2. Delete the old `statusTitle` function at the bottom (it moved to menu.go with new copy).
3. Replace the whole `onReady` closure with:

```go
	onReady := func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("Venom Router")
		systray.SetTooltip("Venom Router")

		mOpenProd := systray.AddMenuItem("Open Production Dashboard", "Open the production dashboard in your browser")
		mStatus := systray.AddMenuItem(statusTitle(c.Status()), "")
		mStatus.Disable()
		mStartProd := systray.AddMenuItem("Start Production", "Start the production server")
		mStopProd := systray.AddMenuItem("Stop Production", "Stop the production server without exiting")
		mRestartProd := systray.AddMenuItem("Restart Production", "Restart the production server")
		systray.AddSeparator()
		mOpenDev := systray.AddMenuItem("Open Development Dashboard", "Open the vite dev server in your browser")
		mDevStatus := systray.AddMenuItem(dev.StatusLine(), "")
		mDevStatus.Disable()
		mStartDev := systray.AddMenuItem("Start Development", "Start the dev frontend (vite) and dev backend")
		mStopDev := systray.AddMenuItem("Stop Development", "Stop the dev frontend and backend")
		mRestartDev := systray.AddMenuItem("Restart Development", "Restart the dev frontend and backend")
		systray.AddSeparator()
		mLogs := systray.AddMenuItem("View Logs", "Open the log file")
		mAutostart := systray.AddMenuItemCheckbox("Start with Windows", "Launch Venom Router automatically when you sign in", autostartEnabled())
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Stop the server and exit")

		apply := func(items [4]*systray.MenuItem, e menuEnablement) {
			set := func(m *systray.MenuItem, on bool) {
				if on {
					m.Enable()
					return
				}
				m.Disable()
			}
			set(items[0], e.Open)
			set(items[1], e.Start)
			set(items[2], e.Stop)
			set(items[3], e.Restart)
		}
		prodItems := [4]*systray.MenuItem{mOpenProd, mStartProd, mStopProd, mRestartProd}
		devItems := [4]*systray.MenuItem{mOpenDev, mStartDev, mStopDev, mRestartDev}

		syncMenu := func() {
			apply(prodItems, prodEnablement(c.Status().State))
			apply(devItems, devEnablement(dev.Available(), dev.Status()))
			mStatus.SetTitle(statusTitle(c.Status()))
			mDevStatus.SetTitle(dev.StatusLine())
			systray.SetTooltip(statusTitle(c.Status()))
		}
		syncMenu()

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-mOpenProd.ClickedCh:
					c.OpenDashboard()
				case <-mStartProd.ClickedCh:
					go c.Start(ctx) // don't block the UI goroutine
				case <-mStopProd.ClickedCh:
					go c.Stop()
				case <-mRestartProd.ClickedCh:
					go c.Restart(ctx)
				case <-mOpenDev.ClickedCh:
					c.OpenURL(dev.DashboardURL())
				case <-mStartDev.ClickedCh:
					go dev.Start()
				case <-mStopDev.ClickedCh:
					go dev.Stop()
				case <-mRestartDev.ClickedCh:
					go dev.Restart()
				case <-mLogs.ClickedCh:
					c.OpenLogs()
				case <-mAutostart.ClickedCh:
					go toggleAutostart(c, mAutostart)
				case <-mQuit.ClickedCh:
					cancel() // funnel into the single ctx.Done() watcher
					return
				case <-ticker.C:
					c.Refresh(ctx)
					dev.Refresh(ctx)
					syncMenu()
				}
			}
		}()
	}
```

4. Extract the existing autostart-toggle closure body into a package function (same file), unchanged logic:

```go
// toggleAutostart flips the start-with-Windows registration and re-syncs the
// checkbox from the actual registry state.
func toggleAutostart(c *Controller, m *systray.MenuItem) {
	var err error
	if m.Checked() {
		err = disableAutostart()
	} else {
		err = enableAutostart()
	}
	if err != nil {
		c.log.Error("tray: toggling start-with-Windows failed",
			observability.String("err", err.Error()))
	}
	if autostartEnabled() {
		m.Check()
		return
	}
	m.Uncheck()
}
```

5. In `internal/tray/tray_other.go`, update the signature to match (parameter unused):

```go
func RunNativeUI(ctx context.Context, _ context.CancelFunc, c *Controller, _ *DevSupervisor) error {
```

6. In `internal/tray/tray_other_test.go:15` (a `!windows` file — it will NOT compile on this Windows host, but Linux CI builds it; fix it blind and carefully), update the call to the new arity:

```go
		done <- RunNativeUI(ctx, cancel, NewController(fakeLC{}, noopOpener{}, Options{Exit: func(int) {}}), NewDevSupervisor(DevSupervisorOptions{}))
```

Cross-check for any other non-Windows caller: `grep -rn "RunNativeUI(" internal/` must show only `tray_windows.go`, `tray_other.go`, `tray_other_test.go`, and `cli.go` — all updated to 4 arguments.

- [ ] **Step 5: Wire the supervisor in `internal/cli/cli.go`**

In `runTrayLoop`, after `ctrl := tray.NewController(...)` and before `lc.Boot`, add — and pass `dev` to `RunNativeUI`:

```go
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		return fmt.Errorf("cli: resolve data dir: %w", err)
	}
	dev := tray.NewDevSupervisor(tray.DevSupervisorOptions{
		Root:    tray.ResolveDevRoot(),
		DataDir: dataDir,
		Runner:  tray.NewProcessRunner(),
		Probe:   tray.DefaultHealthProbe,
		Logger:  logger,
	})
```

and change the call to `tray.RunNativeUI(ctx, cancel, ctrl, dev)`.

- [ ] **Step 6: Run the full package tests**

Run: `go test ./internal/tray/ ./internal/cli/ -v`
Expected: PASS. If any existing test fails on the `RunNativeUI` arity, fix the call site with a `nil`-free supervisor: `tray.NewDevSupervisor(tray.DevSupervisorOptions{})` (unavailable, inert).

- [ ] **Step 7: Build everything**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/tray/menu.go internal/tray/menu_test.go internal/tray/tray_windows.go internal/tray/tray_other.go internal/tray/tray.go internal/cli/cli.go
git commit -m "feat(tray): production/development two-section menu wired to the dev supervisor"
```

---

### Task 7: Full verification + manual visual runbook

**Files:**
- None created; verification only.

- [ ] **Step 1: Full test sweep**

Run from the repo root:
```bash
gofmt -l .
go vet ./...
go test ./...
```
Expected: no gofmt output, vet clean, ALL packages pass (including the pre-existing tray exit/controller suites — their semantics must be untouched). Note the known load-sensitive flake `TestExit_HangShutdown_BoundedNonZero` (20s bound); rerun once if it trips.

- [ ] **Step 2: Dashboard checks**

Run in `dashboard/`: `npm run typecheck && npm run build`
Expected: both green.

- [ ] **Step 3: Manual visual verification (report results, do not skip)**

```bash
go build -o venom-tray-test.exe ./cmd/venom
./venom-tray-test.exe
```

Verify against the two screenshots and record each item in the report:
1. Tray icon is the green triangle on a dark rounded square.
2. Menu order and copy match the approved layout exactly (Production block, separator, Development block, separator, View Logs / Start with Windows / Quit).
3. With the repo as cwd (or `VENOM_DEV_ROOT` set): Start Development spawns vite + dev backend; `Dev Status` walks Starting → Running; Open Development Dashboard opens `http://127.0.0.1:5173/`.
4. Stop Development: both die; `tasklist | findstr /i "node.exe"` shows no leftover vite node processes.
5. Quit the tray while Development is running: dev processes die with it (job kill-on-close).
6. Delete `venom-tray-test.exe` afterwards; do not commit it.

- [ ] **Step 4: Final commit check**

`git status` must be clean except intended changes; every task already committed. Do NOT push — the governor pushes after review.

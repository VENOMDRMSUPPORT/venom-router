package tray

import "context"

// This file is the platform-neutral orchestration that composes the two
// existing lifecycles — production (Controller) and the dev children
// (DevSupervisor) — into the tray's "Start Development" / "Stop Development"
// actions. It lives apart from the Windows systray adapter so the ordering
// rules that keep the one shared database safe are unit-testable on any OS.
//
// The single invariant behind both functions: only ONE process may hold the
// one canonical database (its single-instance lock + its 8081 listener) at a
// time. Production and the dev backend watcher each take that lock, so the two
// must never overlap — the prior corruption incident was two processes on the
// one DB, not a single clean kill.

// prodLifecycle is production as the dev-mode orchestration needs it —
// satisfied by *Controller.
type prodLifecycle interface {
	Stop()
	Start(ctx context.Context)
	Status() StatusView
}

// devLifecycle is the dev children supervisor as the orchestration needs it —
// satisfied by *DevSupervisor.
type devLifecycle interface {
	Available() bool
	Start()
	Stop()
}

// compile-time guards: the real types must satisfy the orchestration interfaces.
var (
	_ prodLifecycle = (*Controller)(nil)
	_ devLifecycle  = (*DevSupervisor)(nil)
)

// EnterDevMode switches from production into the live-reload dev environment:
// it stops production gracefully (freeing 8081 + the single-instance lock),
// then starts the dev children (frontend + backend watcher) against the one
// DB. If production did not reach Stopped — a dirty or timed-out shutdown that
// may still hold the lock — the dev children are deliberately NOT started:
// starting the backend watcher against a DB another process might still hold
// is exactly the two-writer hazard to avoid. No-op when the dev root is
// unavailable, leaving production untouched.
func EnterDevMode(ctx context.Context, prod prodLifecycle, dev devLifecycle) {
	if !dev.Available() {
		return
	}
	prod.Stop()
	if prod.Status().State != StateStopped {
		return
	}
	dev.Start()
}

// ExitDevMode returns to production: it stops the dev children — DevSupervisor
// blocks until the backend's process tree is fully dead, so the lock is free
// and the DB quiescent — then re-boots production, which re-acquires the lock
// and re-opens the one DB.
func ExitDevMode(ctx context.Context, prod prodLifecycle, dev devLifecycle) {
	dev.Stop()
	prod.Start(ctx)
}

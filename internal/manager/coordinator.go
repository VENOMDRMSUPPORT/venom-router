package manager

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Coordinator owns lifecycle state for all managed service groups. It is
// deliberately product-agnostic: Router and Catalog differences live in their
// definitions and adapters, not in duplicated supervisors.
type Coordinator struct {
	mu          sync.RWMutex
	definitions map[GroupID]GroupDefinition
	groups      map[GroupID]*groupRuntime
	runner      ProcessRunner
	now         func() time.Time
	sequence    atomic.Uint64
	events      chan Event
}

type groupRuntime struct {
	definition GroupDefinition
	snapshot   Snapshot
	services   map[ServiceID]*serviceRuntime
	cancel     context.CancelFunc
}

type serviceRuntime struct {
	definition ServiceDefinition
	handle     ProcessHandle
	done       chan struct{}
	state      State
	startedAt  time.Time
	readyAt    time.Time
	detail     string
}

type stopTarget struct {
	definition ServiceDefinition
	handle     ProcessHandle
	done       chan struct{}
}

// NewCoordinator validates and registers the supplied service groups.
func NewCoordinator(definitions []GroupDefinition, runner ProcessRunner) (*Coordinator, error) {
	if runner == nil {
		return nil, fmt.Errorf("%w: process runner is nil", ErrInvalidDefinition)
	}
	if err := ValidateDefinitions(definitions); err != nil {
		return nil, err
	}
	c := &Coordinator{
		definitions: make(map[GroupID]GroupDefinition, len(definitions)),
		groups:      make(map[GroupID]*groupRuntime, len(definitions)),
		runner:      runner,
		now:         time.Now,
		events:      make(chan Event, 256),
	}
	for _, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return nil, err
		}
		if _, exists := c.definitions[definition.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate group %q", ErrInvalidDefinition, definition.ID)
		}
		c.definitions[definition.ID] = definition
		services := make(map[ServiceID]*serviceRuntime, len(definition.Services))
		for _, service := range definition.Services {
			services[service.ID] = &serviceRuntime{definition: service, state: StateStopped}
		}
		c.groups[definition.ID] = &groupRuntime{
			definition: definition,
			services:   services,
			snapshot: Snapshot{
				GroupID:     definition.ID,
				Product:     definition.Product,
				Environment: definition.Environment,
				State:       StateStopped,
				Services:    serviceSnapshots(services),
			},
		}
	}
	return c, nil
}

// Events returns a best-effort stream of secret-free lifecycle events. Events
// are intentionally buffered so a slow UI cannot block process supervision.
func (c *Coordinator) Events() <-chan Event { return c.events }

// Snapshot returns a consistent copy of one group's current state.
func (c *Coordinator) Snapshot(id GroupID) (Snapshot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	group, ok := c.groups[id]
	if !ok {
		return Snapshot{}, ErrUnknownGroup
	}
	return copySnapshot(group.snapshot), nil
}

// Snapshots returns a consistent copy of all registered groups.
func (c *Coordinator) Snapshots() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Snapshot, 0, len(c.groups))
	for _, group := range c.groups {
		out = append(out, copySnapshot(group.snapshot))
	}
	return out
}

// Start schedules a non-blocking start operation and returns its identifier.
func (c *Coordinator) Start(id GroupID) (OperationID, error) {
	c.mu.Lock()
	group, ok := c.groups[id]
	if !ok {
		c.mu.Unlock()
		return "", ErrUnknownGroup
	}
	if group.snapshot.State == StatePreparing || group.snapshot.State == StateStarting || group.snapshot.State == StateWaitingReadiness || group.snapshot.State == StateStopping || (group.snapshot.State == StateError && hasLiveServices(group)) {
		c.mu.Unlock()
		return "", ErrOperationInProgress
	}
	for _, conflictID := range group.definition.Conflicts {
		conflict, exists := c.groups[conflictID]
		if exists && isGroupActive(conflict) {
			c.mu.Unlock()
			return "", fmt.Errorf("%w: %s", ErrConflictingGroup, conflictID)
		}
	}
	op := c.nextOperationID()
	group.snapshot.OperationID = op
	group.snapshot.StartedAt = c.now()
	c.transitionLocked(group, StatePreparing, "start_requested", "operation accepted")
	ctx, cancel := context.WithCancel(context.Background())
	group.cancel = cancel
	c.mu.Unlock()

	go c.startGroup(ctx, id, op)
	return op, nil
}

// Stop schedules a non-blocking bounded stop operation and returns its ID.
func (c *Coordinator) Stop(id GroupID) (OperationID, error) {
	c.mu.Lock()
	group, ok := c.groups[id]
	if !ok {
		c.mu.Unlock()
		return "", ErrUnknownGroup
	}
	if group.snapshot.State == StateStopped {
		c.mu.Unlock()
		return "", nil
	}
	if group.snapshot.State == StateStopping {
		c.mu.Unlock()
		return "", ErrOperationInProgress
	}
	op := c.nextOperationID()
	group.snapshot.OperationID = op
	c.transitionLocked(group, StateStopping, "stop_requested", "shutdown scheduled")
	cancel := group.cancel
	if cancel != nil {
		cancel()
	}
	targets := make([]stopTarget, 0, len(group.services))
	for _, service := range group.services {
		targets = append(targets, stopTarget{
			definition: service.definition,
			handle:     service.handle,
			done:       service.done,
		})
	}
	c.mu.Unlock()

	go c.stopGroup(id, op, targets)
	return op, nil
}

// StopAndWait requests a stop and waits for the group to reach stopped or
// the supplied context to expire. It is used by process-owner shutdown paths,
// never by the UI request handler.
func (c *Coordinator) StopAndWait(ctx context.Context, id GroupID) error {
	if _, err := c.Stop(id); err != nil {
		return err
	}
	for {
		snapshot, err := c.Snapshot(id)
		if err != nil {
			return err
		}
		if snapshot.State == StateStopped {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Restart schedules a stop-then-start operation. A stopped group is started
// directly; an active group is stopped first and then started after all process
// trees have exited.
func (c *Coordinator) Restart(id GroupID) (OperationID, error) {
	c.mu.RLock()
	group, ok := c.groups[id]
	if !ok {
		c.mu.RUnlock()
		return "", ErrUnknownGroup
	}
	state := group.snapshot.State
	c.mu.RUnlock()
	if state == StatePreparing || state == StateStarting || state == StateWaitingReadiness || state == StateStopping {
		return "", ErrOperationInProgress
	}
	if state == StateStopped || state == StateError {
		return c.Start(id)
	}
	stopID, err := c.Stop(id)
	if err != nil {
		return "", err
	}
	go func() {
		for {
			snapshot, snapshotErr := c.Snapshot(id)
			if snapshotErr != nil || snapshot.State == StateStopped {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = c.Start(id)
	}()
	return stopID, nil
}

func (c *Coordinator) startGroup(ctx context.Context, id GroupID, op OperationID) {
	c.transition(id, op, StateStarting, "process_launch", "starting process group")

	c.mu.RLock()
	group := c.groups[id]
	definitions := append([]ServiceDefinition(nil), group.definition.Services...)
	c.mu.RUnlock()

	var wg sync.WaitGroup
	for _, definition := range definitions {
		definition := definition
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.startService(id, op, definition)
		}()
	}
	wg.Wait()

	c.mu.RLock()
	group = c.groups[id]
	failed := false
	for _, service := range group.services {
		if service.handle == nil && service.definition.Required {
			failed = true
			break
		}
	}
	c.mu.RUnlock()
	if failed {
		c.failAndStop(id, op, "process_start_failed", "a required service failed to start")
		return
	}

	c.transition(id, op, StateWaitingReadiness, "readiness_wait", "waiting for service readiness")
	c.waitReadiness(ctx, id, op, definitions)
}

func (c *Coordinator) startService(id GroupID, op OperationID, definition ServiceDefinition) {
	handle, err := c.runner.Start(definition.Spec)
	c.mu.Lock()
	defer c.mu.Unlock()
	group := c.groups[id]
	service := group.services[definition.ID]
	if group.snapshot.OperationID != op {
		if err == nil {
			_ = handle.Kill()
		}
		return
	}
	if err != nil {
		service.state = StateError
		service.detail = "process failed to start"
		return
	}
	service.handle = handle
	service.done = make(chan struct{})
	service.startedAt = c.now()
	service.state = StateStarting
	go c.watchService(id, op, definition.ID, handle, service.done)
}

func (c *Coordinator) waitReadiness(ctx context.Context, id GroupID, op OperationID, definitions []ServiceDefinition) {
	var wg sync.WaitGroup
	for _, definition := range definitions {
		definition := definition
		wg.Add(1)
		go func() {
			defer wg.Done()
			serviceCtx := ctx
			if definition.StartDeadline > 0 {
				var cancel context.CancelFunc
				serviceCtx, cancel = context.WithTimeout(ctx, definition.StartDeadline)
				defer cancel()
			}
			for _, check := range definition.Readiness {
				if check == nil {
					continue
				}
				if !waitForReadiness(serviceCtx, check) {
					c.markServiceFailure(id, op, definition.ID, "readiness_failed")
					return
				}
			}
			c.markServiceReady(id, op, definition.ID)
		}()
	}
	wg.Wait()

	c.mu.RLock()
	group := c.groups[id]
	failed := false
	degraded := false
	for _, service := range group.services {
		switch service.state {
		case StateError:
			if service.definition.Required {
				failed = true
			} else {
				degraded = true
			}
		case StateStarting, StateWaitingReadiness:
			if service.definition.Required {
				failed = true
			}
		}
	}
	c.mu.RUnlock()
	if failed {
		c.failAndStop(id, op, "readiness_failed", "a required service did not become ready")
		return
	}
	if degraded {
		c.transition(id, op, StateDegraded, "optional_service_unready", "required services are ready")
		return
	}
	c.transition(id, op, StateReady, "ready", "all required services are ready")
}

func waitForReadiness(ctx context.Context, check ReadinessCheck) bool {
	for {
		if err := check(ctx); err == nil {
			return true
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (c *Coordinator) markServiceReady(id GroupID, op OperationID, serviceID ServiceID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	group := c.groups[id]
	if group.snapshot.OperationID != op {
		return
	}
	service := group.services[serviceID]
	service.state = StateReady
	service.readyAt = c.now()
	service.detail = "ready"
	group.snapshot.Services = serviceSnapshots(group.services)
	group.snapshot.UpdatedAt = c.now()
}

func (c *Coordinator) markServiceFailure(id GroupID, op OperationID, serviceID ServiceID, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	group := c.groups[id]
	if group.snapshot.OperationID != op {
		return
	}
	service := group.services[serviceID]
	service.state = StateError
	service.detail = reason
	group.snapshot.Services = serviceSnapshots(group.services)
	group.snapshot.UpdatedAt = c.now()
}

func (c *Coordinator) failAndStop(id GroupID, op OperationID, reason, detail string) {
	c.transition(id, op, StateError, reason, detail)
	c.mu.Lock()
	group := c.groups[id]
	targets := make([]stopTarget, 0, len(group.services))
	for _, service := range group.services {
		targets = append(targets, stopTarget{
			definition: service.definition,
			handle:     service.handle,
			done:       service.done,
		})
	}
	c.mu.Unlock()
	for _, target := range targets {
		if target.handle != nil {
			_ = target.handle.Kill()
		}
	}
}

func (c *Coordinator) stopGroup(id GroupID, op OperationID, targets []stopTarget) {
	for _, target := range targets {
		if target.handle == nil {
			continue
		}
		_ = target.handle.Kill()
		deadline := target.definition.StopDeadline
		if deadline <= 0 {
			deadline = 10 * time.Second
		}
		select {
		case <-target.done:
		case <-time.After(deadline):
		}
	}
	c.mu.Lock()
	group := c.groups[id]
	if group.snapshot.OperationID == op {
		for _, service := range group.services {
			service.handle = nil
			service.state = StateStopped
			service.detail = "stopped"
		}
		group.cancel = nil
		c.transitionLocked(group, StateStopped, "stopped", "process group stopped")
	}
	c.mu.Unlock()
}

func (c *Coordinator) watchService(id GroupID, op OperationID, serviceID ServiceID, handle ProcessHandle, done chan struct{}) {
	err := handle.Wait()
	close(done)
	c.mu.Lock()
	defer c.mu.Unlock()
	group := c.groups[id]
	service := group.services[serviceID]
	if group.snapshot.OperationID != op || group.snapshot.State == StateStopping || group.snapshot.State == StateStopped {
		return
	}
	service.handle = nil
	service.state = StateError
	if err == nil {
		service.detail = "process exited unexpectedly"
	} else {
		service.detail = "process exited unexpectedly"
	}
	c.transitionLocked(group, StateError, "unexpected_exit", service.detail)
}

func (c *Coordinator) transition(id GroupID, op OperationID, to State, reason, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	group := c.groups[id]
	if group.snapshot.OperationID != op {
		return
	}
	c.transitionLocked(group, to, reason, detail)
}

func (c *Coordinator) transitionLocked(group *groupRuntime, to State, reason, detail string) {
	from := group.snapshot.State
	now := c.now()
	group.snapshot.State = to
	group.snapshot.Detail = detail
	group.snapshot.UpdatedAt = now
	group.snapshot.Services = serviceSnapshots(group.services)
	c.emit(Event{
		GroupID:     group.definition.ID,
		OperationID: group.snapshot.OperationID,
		From:        from,
		To:          to,
		Phase:       reason,
		ReasonCode:  reason,
		Detail:      detail,
		OccurredAt:  now,
		Elapsed:     elapsed(group.snapshot.StartedAt, now),
	})
}

func (c *Coordinator) emit(event Event) {
	select {
	case c.events <- event:
	default:
		// The lifecycle state remains authoritative in Snapshot. Dropping an
		// old event is safer than blocking process supervision on a UI consumer.
	}
}

func (c *Coordinator) nextOperationID() OperationID {
	return OperationID(fmt.Sprintf("op-%d-%d", c.now().UnixNano(), c.sequence.Add(1)))
}

func isGroupActive(group *groupRuntime) bool {
	return group.snapshot.State.IsActive() || hasLiveServices(group)
}

func hasLiveServices(group *groupRuntime) bool {
	for _, service := range group.services {
		if service.handle != nil {
			return true
		}
	}
	return false
}

func serviceSnapshots(services map[ServiceID]*serviceRuntime) []ServiceSnapshot {
	out := make([]ServiceSnapshot, 0, len(services))
	for _, service := range services {
		out = append(out, ServiceSnapshot{
			ID:        service.definition.ID,
			Name:      service.definition.Name,
			State:     service.state,
			Detail:    service.detail,
			StartedAt: service.startedAt,
			ReadyAt:   service.readyAt,
			Ports:     append([]string(nil), service.definition.Ports...),
		})
	}
	return out
}

func copySnapshot(snapshot Snapshot) Snapshot {
	snapshot.Services = append([]ServiceSnapshot(nil), snapshot.Services...)
	return snapshot
}

func elapsed(start, end time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return end.Sub(start)
}

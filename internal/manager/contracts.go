// Package manager contains the local desktop orchestration core for venom.exe.
package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GroupID identifies one product/environment service group.
type GroupID string

// ServiceID identifies one process inside a service group.
type ServiceID string

// OperationID identifies one asynchronous lifecycle operation.
type OperationID string

// Product identifies a managed product without coupling the manager to its domain.
type Product string

const (
	ProductRouter  Product = "router"
	ProductCatalog Product = "catalog"
)

// Environment identifies a managed deployment profile.
type Environment string

const (
	EnvironmentProduction  Environment = "production"
	EnvironmentDevelopment Environment = "development"
)

// State is the externally visible lifecycle state of a service group.
type State string

const (
	StateStopped          State = "stopped"
	StatePreparing        State = "preparing"
	StateStarting         State = "starting"
	StateWaitingReadiness State = "waiting_readiness"
	StateReady            State = "ready"
	StateDegraded         State = "degraded"
	StateStopping         State = "stopping"
	StateError            State = "error"
)

// ProcessSpec describes one child process without embedding OS-specific logic.
type ProcessSpec struct {
	WorkingDir string
	Command    string
	Args       []string
	Env        []string
	LogPath    string
}

// ProcessHandle is the manager's minimal view of a child process tree.
type ProcessHandle interface {
	Wait() error
	Kill() error
}

// ProcessRunner creates a contained process tree.
type ProcessRunner interface {
	Start(spec ProcessSpec) (ProcessHandle, error)
}

// ReadinessCheck verifies one service-level readiness condition. It must return
// nil only when the condition is ready and must not expose credentials in errors.
type ReadinessCheck func(context.Context) error

// ServiceDefinition describes one process and its readiness requirements.
type ServiceDefinition struct {
	ID            ServiceID
	Name          string
	Spec          ProcessSpec
	Readiness     []ReadinessCheck
	Required      bool
	StartDeadline time.Duration
	StopDeadline  time.Duration
	DataRoot      string
	Ports         []string
}

// GroupDefinition describes one product/environment and its safety boundaries.
type GroupDefinition struct {
	ID            GroupID
	Product       Product
	Environment   Environment
	Services      []ServiceDefinition
	Conflicts     []GroupID
	DashboardURL  string
	RequiredTools []string
}

// Event records one secret-free lifecycle transition or timing marker.
type Event struct {
	GroupID     GroupID
	OperationID OperationID
	ServiceID   ServiceID
	From        State
	To          State
	Phase       string
	ReasonCode  string
	Detail      string
	OccurredAt  time.Time
	Elapsed     time.Duration
}

// Snapshot is a consistent, UI-ready group state.
type Snapshot struct {
	GroupID     GroupID
	Product     Product
	Environment Environment
	State       State
	OperationID OperationID
	StartedAt   time.Time
	UpdatedAt   time.Time
	Detail      string
	Services    []ServiceSnapshot
}

// ServiceSnapshot is the current state of one child process.
type ServiceSnapshot struct {
	ID        ServiceID
	Name      string
	State     State
	PID       uint32
	Detail    string
	StartedAt time.Time
	ReadyAt   time.Time
	Ports     []string
}

var (
	ErrUnknownGroup        = errors.New("manager: unknown service group")
	ErrInvalidDefinition   = errors.New("manager: invalid service definition")
	ErrOperationInProgress = errors.New("manager: operation already in progress")
	ErrConflictingGroup    = errors.New("manager: conflicting service group is active")
	ErrNotReady            = errors.New("manager: service group is not ready")
)

// Validate checks a service group before any process is created.
func (d GroupDefinition) Validate() error {
	if strings.TrimSpace(string(d.ID)) == "" || strings.TrimSpace(string(d.Product)) == "" || strings.TrimSpace(string(d.Environment)) == "" {
		return fmt.Errorf("%w: group identity is incomplete", ErrInvalidDefinition)
	}
	if len(d.Services) == 0 {
		return fmt.Errorf("%w: group %q has no services", ErrInvalidDefinition, d.ID)
	}
	seen := make(map[ServiceID]struct{}, len(d.Services))
	for _, service := range d.Services {
		if strings.TrimSpace(string(service.ID)) == "" || strings.TrimSpace(service.Name) == "" || strings.TrimSpace(service.Spec.Command) == "" {
			return fmt.Errorf("%w: service identity or command is incomplete", ErrInvalidDefinition)
		}
		if _, ok := seen[service.ID]; ok {
			return fmt.Errorf("%w: duplicate service %q", ErrInvalidDefinition, service.ID)
		}
		seen[service.ID] = struct{}{}
		if service.StartDeadline < 0 || service.StopDeadline < 0 {
			return fmt.Errorf("%w: negative deadline for service %q", ErrInvalidDefinition, service.ID)
		}
	}
	return nil
}

// IsActive reports whether a group may own live processes.
func (s State) IsActive() bool {
	return s != StateStopped && s != StateError
}

// IsTerminal reports whether a lifecycle operation has reached a stable state.
func (s State) IsTerminal() bool {
	return s == StateStopped || s == StateReady || s == StateDegraded || s == StateError
}

package manager

import (
	"errors"
	"testing"
	"time"
)

func validGroup() GroupDefinition {
	return GroupDefinition{
		ID:          "router.development",
		Product:     ProductRouter,
		Environment: EnvironmentDevelopment,
		Services: []ServiceDefinition{{
			ID:            "router.backend",
			Name:          "Router backend",
			Spec:          ProcessSpec{Command: "venom"},
			StartDeadline: time.Minute,
			StopDeadline:  10 * time.Second,
			Required:      true,
		}},
	}
}

func TestGroupDefinitionValidateAcceptsCompleteDefinition(t *testing.T) {
	if err := validGroup().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestGroupDefinitionValidateRejectsDuplicateService(t *testing.T) {
	group := validGroup()
	group.Services = append(group.Services, group.Services[0])
	if err := group.Validate(); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDefinition", err)
	}
}

func TestGroupDefinitionValidateRejectsMissingCommand(t *testing.T) {
	group := validGroup()
	group.Services[0].Spec.Command = ""
	if err := group.Validate(); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDefinition", err)
	}
}

func TestStateActivityAndTerminalSemantics(t *testing.T) {
	if !StateWaitingReadiness.IsActive() {
		t.Fatal("waiting_readiness must be active")
	}
	if StateError.IsActive() {
		t.Fatal("error must not be active")
	}
	if !StateReady.IsTerminal() || StateStarting.IsTerminal() {
		t.Fatal("ready must be terminal and starting must not be terminal")
	}
}

func TestValidateDefinitionsRejectsPortCollision(t *testing.T) {
	first := validGroup()
	first.ID = "router.production"
	first.Services[0].Ports = []string{"8081"}
	second := validGroup()
	second.ID = "router.development"
	second.Services[0].Ports = []string{"8081"}

	if err := ValidateDefinitions([]GroupDefinition{first, second}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("ValidateDefinitions() error = %v, want ErrInvalidDefinition", err)
	}
}

func TestValidateDefinitionsRejectsDataRootCollision(t *testing.T) {
	first := validGroup()
	first.ID = "catalog.production"
	first.Services[0].DataRoot = "catalog.db"
	second := validGroup()
	second.ID = "catalog.development"
	second.Services[0].DataRoot = "catalog.db"

	if err := ValidateDefinitions([]GroupDefinition{first, second}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("ValidateDefinitions() error = %v, want ErrInvalidDefinition", err)
	}
}

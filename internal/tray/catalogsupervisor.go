package tray

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/manager"
)

const (
	catalogProductionAPI  = "http://127.0.0.1:8791"
	catalogDevelopmentAPI = "http://127.0.0.1:8792"
	catalogProductionUI   = "http://127.0.0.1:5173/"
	catalogDevelopmentUI  = "http://127.0.0.1:5174/"
)

type managerRunner struct{ runner ProcessRunner }

func (r managerRunner) Start(spec manager.ProcessSpec) (manager.ProcessHandle, error) {
	if r.runner == nil {
		return nil, fmt.Errorf("tray: catalog process runner is unavailable")
	}
	handle, err := r.runner.Start(ProcessSpec{
		Dir:        spec.WorkingDir,
		Name:       spec.Command,
		Args:       append([]string(nil), spec.Args...),
		ExtraEnv:   append([]string(nil), spec.Env...),
		OutputPath: spec.LogPath,
	})
	if err != nil {
		return nil, err
	}
	return handle, nil
}

type catalogService struct {
	manager.ProcessHandle
}

type CatalogSupervisor struct {
	coordinator *manager.Coordinator
	production  *manager.CatalogAdapter
	development *manager.CatalogAdapter
}

// NewCatalogSupervisor builds two fully independent Catalog groups. The
// manager owns only process lifecycle; Catalog remains the database writer.
func NewCatalogSupervisor(root, dataDir, logDir string, runner ProcessRunner) (*CatalogSupervisor, error) {
	if root == "" {
		return nil, nil
	}
	catalogRoot := filepath.Join(root, "catalog")
	production, err := manager.NewCatalogAdapter(catalogProductionAPI, nil)
	if err != nil {
		return nil, err
	}
	development, err := manager.NewCatalogAdapter(catalogDevelopmentAPI, nil)
	if err != nil {
		return nil, err
	}
	productionGroup := catalogGroup(
		"catalog.production", manager.EnvironmentProduction, catalogRoot,
		filepath.Join(catalogRoot, "data", "catalog.db"),
		catalogProductionAPI, catalogProductionUI,
		filepath.Join(logDir, "catalog-production.log"), false,
	)
	developmentGroup := catalogGroup(
		"catalog.development", manager.EnvironmentDevelopment, catalogRoot,
		filepath.Join(dataDir, "catalog-development.db"),
		catalogDevelopmentAPI, catalogDevelopmentUI,
		filepath.Join(logDir, "catalog-development.log"), true,
	)
	coordinator, err := manager.NewCoordinator([]manager.GroupDefinition{productionGroup, developmentGroup}, managerRunner{runner: runner})
	if err != nil {
		return nil, err
	}
	return &CatalogSupervisor{coordinator: coordinator, production: production, development: development}, nil
}

func catalogGroup(id manager.GroupID, environment manager.Environment, root, dbPath, apiBase, uiURL, logPath string, development bool) manager.GroupDefinition {
	apiPort := "8791"
	uiPort := "5173"
	if development {
		apiPort = "8792"
		uiPort = "5174"
	}
	uiArgs := []string{"preview", "--host", "127.0.0.1", "--port", uiPort, "--strictPort"}
	if development {
		uiArgs = []string{"--host", "127.0.0.1", "--port", uiPort, "--strictPort"}
	}
	adapter, _ := manager.NewCatalogAdapter(apiBase, nil)
	return manager.GroupDefinition{
		ID:          id,
		Product:     manager.ProductCatalog,
		Environment: environment,
		Services: []manager.ServiceDefinition{
			{
				ID:            manager.ServiceID(string(id) + ".api"),
				Name:          "Catalog API",
				Required:      true,
				Spec:          manager.ProcessSpec{WorkingDir: root, Command: "node", Args: []string{"server/index.ts", "--port=" + apiPort}, Env: []string{"CATALOG_DB=" + dbPath}, LogPath: logPath},
				Readiness:     []manager.ReadinessCheck{adapter.ReadyCheck},
				StartDeadline: 45 * time.Second,
				StopDeadline:  10 * time.Second,
				DataRoot:      dbPath,
				Ports:         []string{apiPort},
			},
			{
				ID:            manager.ServiceID(string(id) + ".ui"),
				Name:          "Catalog UI",
				Required:      true,
				Spec:          manager.ProcessSpec{WorkingDir: root, Command: "node", Args: append([]string{"node_modules/vite/bin/vite.js"}, uiArgs...), Env: []string{"CATALOG_API=" + apiBase}, LogPath: logPath},
				Readiness:     []manager.ReadinessCheck{urlReadyCheck(uiURL)},
				StartDeadline: 45 * time.Second,
				StopDeadline:  10 * time.Second,
				Ports:         []string{uiPort},
			},
		},
	}
}

func urlReadyCheck(url string) manager.ReadinessCheck {
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("manager: UI readiness request failed")
		}
		client := &http.Client{Timeout: time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("manager: UI is not ready")
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 500 {
			return fmt.Errorf("manager: UI is not ready")
		}
		return nil
	}
}

func (s *CatalogSupervisor) StartProduction() {
	if s != nil {
		_, _ = s.coordinator.Start("catalog.production")
	}
}
func (s *CatalogSupervisor) StopProduction() {
	if s != nil {
		_, _ = s.coordinator.Stop("catalog.production")
	}
}
func (s *CatalogSupervisor) RestartProduction() {
	if s != nil {
		_, _ = s.coordinator.Restart("catalog.production")
	}
}
func (s *CatalogSupervisor) StartDevelopment() {
	if s != nil {
		_, _ = s.coordinator.Start("catalog.development")
	}
}
func (s *CatalogSupervisor) StopDevelopment() {
	if s != nil {
		_, _ = s.coordinator.Stop("catalog.development")
	}
}
func (s *CatalogSupervisor) RestartDevelopment() {
	if s != nil {
		_, _ = s.coordinator.Restart("catalog.development")
	}
}

func (s *CatalogSupervisor) State(id manager.GroupID) string {
	if s == nil {
		return "unavailable"
	}
	snapshot, err := s.coordinator.Snapshot(id)
	if err != nil {
		return "unavailable"
	}
	return string(snapshot.State)
}

func (s *CatalogSupervisor) Shutdown() {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.coordinator.StopAndWait(ctx, "catalog.development")
	_ = s.coordinator.StopAndWait(ctx, "catalog.production")
}

func (s *CatalogSupervisor) ProductionAdapter() *manager.CatalogAdapter {
	if s == nil {
		return nil
	}
	return s.production
}
func (s *CatalogSupervisor) DevelopmentAdapter() *manager.CatalogAdapter {
	if s == nil {
		return nil
	}
	return s.development
}

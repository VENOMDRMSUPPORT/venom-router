package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CatalogAdapter is the manager's read-only boundary to a standalone Catalog
// service. It never opens the Catalog database and accepts stale/degraded HTTP
// responses when the service returns a valid health document.
type CatalogAdapter struct {
	baseURL string
	client  *http.Client
}

// NewCatalogAdapter creates a loopback Catalog API client.
func NewCatalogAdapter(baseURL string, client *http.Client) (*CatalogAdapter, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("manager: catalog base URL is empty")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return &CatalogAdapter{baseURL: baseURL, client: client}, nil
}

// CatalogHealth is a server-owned health/freshness projection. Unknown fields
// remain nil/zero and are never converted into a healthy result by the client.
type CatalogHealth struct {
	HTTPStatus         int
	APIContractVersion string
	ServiceStatus      string
	DatabaseReadable   bool
	StartedAt          *time.Time
	SyncInFlight       bool
	SchedulerEnabled   bool
	NextScheduledRunAt *time.Time
	CatalogStatus      string
	LiveModels         int
	MethodologyVersion string
	StaleAfterHours    int
	StaleProviders     []CatalogProviderHealth
	Providers          []CatalogProviderHealth
	LastSync           *CatalogLastSync
}

type CatalogProviderHealth struct {
	ID                   string
	LiveModels           int
	Freshness            string
	LastSuccessfulSyncAt *time.Time
	LastAttemptedSyncAt  *time.Time
	LastOutcome          string
	HoursSinceSuccess    float64
}

type CatalogLastSync struct {
	StartedAt  *time.Time
	FinishedAt *time.Time
	Aborted    *bool
}

type catalogHealthResponse struct {
	API struct {
		ContractVersion string `json:"contractVersion"`
	} `json:"api"`
	Service struct {
		Status             string     `json:"status"`
		DatabaseReadable   bool       `json:"databaseReadable"`
		StartedAt          *time.Time `json:"startedAt"`
		SyncInFlight       bool       `json:"syncInFlight"`
		SchedulerEnabled   bool       `json:"schedulerEnabled"`
		NextScheduledRunAt *time.Time `json:"nextScheduledRunAt"`
	} `json:"service"`
	Catalog struct {
		Status             string                  `json:"status"`
		LiveModels         int                     `json:"liveModels"`
		MethodologyVersion string                  `json:"methodologyVersion"`
		StaleAfterHours    int                     `json:"staleAfterHours"`
		StaleProviders     []catalogProviderHealth `json:"staleProviders"`
		Providers          []catalogProviderHealth `json:"providers"`
	} `json:"catalog"`
	LastSync *struct {
		StartedAt  *time.Time `json:"startedAt"`
		FinishedAt *time.Time `json:"finishedAt"`
		Aborted    *bool      `json:"aborted"`
	} `json:"lastSync"`
}

type catalogProviderHealth struct {
	ID                   string     `json:"id"`
	LiveModels           int        `json:"liveModels"`
	Freshness            string     `json:"freshness"`
	LastSuccessfulSyncAt *time.Time `json:"lastSuccessfulSyncAt"`
	LastAttemptedSyncAt  *time.Time `json:"lastAttemptedSyncAt"`
	LastOutcome          string     `json:"lastOutcome"`
	HoursSinceSuccess    float64    `json:"hoursSinceSuccess"`
}

// Health reads Catalog's existing /v1/health contract. HTTP 503/500 is a valid
// catalog freshness/service result when the response body is well-formed.
func (a *CatalogAdapter) Health(ctx context.Context) (CatalogHealth, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/v1/health", nil)
	if err != nil {
		return CatalogHealth{}, fmt.Errorf("manager: catalog health request: %w", err)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return CatalogHealth{}, fmt.Errorf("manager: catalog health unavailable")
	}
	defer func() { _ = resp.Body.Close() }()

	var raw catalogHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return CatalogHealth{HTTPStatus: resp.StatusCode}, fmt.Errorf("manager: catalog health response invalid")
	}
	return mapCatalogHealth(resp.StatusCode, raw), nil
}

// ReadyCheck adapts Catalog health into a lifecycle readiness check while
// retaining stale catalog status as degraded rather than hiding it.
func (a *CatalogAdapter) ReadyCheck(ctx context.Context) error {
	health, err := a.Health(ctx)
	if err != nil {
		return err
	}
	if health.ServiceStatus != "up" || !health.DatabaseReadable {
		return fmt.Errorf("manager: catalog service is not ready")
	}
	return nil
}

func mapCatalogHealth(status int, raw catalogHealthResponse) CatalogHealth {
	health := CatalogHealth{
		HTTPStatus:         status,
		APIContractVersion: raw.API.ContractVersion,
		ServiceStatus:      raw.Service.Status,
		DatabaseReadable:   raw.Service.DatabaseReadable,
		StartedAt:          raw.Service.StartedAt,
		SyncInFlight:       raw.Service.SyncInFlight,
		SchedulerEnabled:   raw.Service.SchedulerEnabled,
		NextScheduledRunAt: raw.Service.NextScheduledRunAt,
		CatalogStatus:      raw.Catalog.Status,
		LiveModels:         raw.Catalog.LiveModels,
		MethodologyVersion: raw.Catalog.MethodologyVersion,
		StaleAfterHours:    raw.Catalog.StaleAfterHours,
		StaleProviders:     mapProviderHealth(raw.Catalog.StaleProviders),
		Providers:          mapProviderHealth(raw.Catalog.Providers),
	}
	if raw.LastSync != nil {
		health.LastSync = &CatalogLastSync{
			StartedAt:  raw.LastSync.StartedAt,
			FinishedAt: raw.LastSync.FinishedAt,
			Aborted:    raw.LastSync.Aborted,
		}
	}
	return health
}

func mapProviderHealth(items []catalogProviderHealth) []CatalogProviderHealth {
	out := make([]CatalogProviderHealth, 0, len(items))
	for _, item := range items {
		out = append(out, CatalogProviderHealth{
			ID:                   item.ID,
			LiveModels:           item.LiveModels,
			Freshness:            item.Freshness,
			LastSuccessfulSyncAt: item.LastSuccessfulSyncAt,
			LastAttemptedSyncAt:  item.LastAttemptedSyncAt,
			LastOutcome:          item.LastOutcome,
			HoursSinceSuccess:    item.HoursSinceSuccess,
		})
	}
	return out
}

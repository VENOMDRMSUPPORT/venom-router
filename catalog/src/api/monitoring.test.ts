import { describe, expect, test } from 'vitest';
import type { CatalogData, HealthResponse } from './client';
import { buildMonitoringSignals } from './monitoring';

const data = (over: Partial<CatalogData> = {}): CatalogData => ({
  models: [],
  providers: [],
  meta: { needsVerification: 0 } as CatalogData['meta'],
  origin: 'live',
  ...over,
});

const health = (over: Partial<HealthResponse> = {}): HealthResponse => ({
  service: {
    status: 'up', databaseReadable: true, startedAt: null, syncInFlight: false,
    currentRunStartedAt: null, schedulerEnabled: true, nextScheduledRunAt: null,
  },
  catalog: {
    status: 'current', liveModels: 10, methodologyVersion: 'v1', staleAfterHours: 24,
    staleProviders: [], providers: [],
  },
  lastSync: null,
  ...over,
});

describe('buildMonitoringSignals', () => {
  test('reports an unreachable service as critical and retryable', () => {
    const signals = buildMonitoringSignals({ data: null, health: null, healthError: 'connection refused', healthLoading: false });
    expect(signals).toEqual([expect.objectContaining({ id: 'service-unreachable', severity: 'critical', action: 'retry' })]);
  });

  test('distinguishes stale providers and failed syncs', () => {
    const signals = buildMonitoringSignals({
      data: data({ providers: [{ id: 'openrouter', name: 'OpenRouter' }] as CatalogData['providers'] }),
      health: health({
        catalog: {
          status: 'stale', liveModels: 10, methodologyVersion: 'v1', staleAfterHours: 24,
          staleProviders: [{ id: 'openrouter', freshness: 'stale', lastSuccessfulSyncAt: null, lastOutcome: 'failed' }],
          providers: [],
        },
        lastSync: { startedAt: '2026-08-23T10:00:00Z', finishedAt: '2026-08-23T10:01:00Z', aborted: null, providers: [{ provider: 'openrouter', outcome: 'failed', error: 'timeout' }] },
      }),
      healthError: null,
      healthLoading: false,
    });
    expect(signals.map((signal) => signal.id)).toEqual(['stale-providers', 'sync-failures']);
    expect(signals[0].detail).toContain('OpenRouter');
  });

  test('reports active resolution work without presenting it as a failure', () => {
    const signals = buildMonitoringSignals({
      data: data({ meta: { needsVerification: 2 } as CatalogData['meta'], models: [{ resolution: { state: 'processing' } }] as CatalogData['models'] }),
      health: health({ service: { ...health().service, syncInFlight: true } }),
      healthError: null,
      healthLoading: false,
    });
    expect(signals.map((signal) => signal.severity)).toEqual(['info', 'info', 'info']);
  });

  test('reports all-clear only when monitoring has answered', () => {
    expect(buildMonitoringSignals({ data: data(), health: null, healthError: null, healthLoading: true })).toEqual([]);
    expect(buildMonitoringSignals({ data: data(), health: health(), healthError: null, healthLoading: false })).toEqual([expect.objectContaining({ id: 'all-clear', severity: 'success' })]);
  });
});

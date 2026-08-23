import type { CatalogData, HealthResponse } from './client';

export type MonitoringSeverity = 'critical' | 'warning' | 'info' | 'success';
export type MonitoringAction = 'retry' | 'changes' | 'none';

export interface MonitoringSignal {
  id: string;
  severity: MonitoringSeverity;
  title: string;
  detail: string;
  action: MonitoringAction;
}

export interface MonitoringInput {
  data: CatalogData | null;
  health: HealthResponse | null | undefined;
  healthError: string | null | undefined;
  healthLoading: boolean | undefined;
}

function providerNames(data: CatalogData | null, ids: string[]): string {
  if (!data || ids.length === 0) return '';
  return ids
    .map((id) => data.providers.find((provider) => provider.id === id)?.name ?? id)
    .join(', ');
}

/**
 * Translate service-owned status into concise UI signals.
 *
 * This function only classifies facts already present in the API response. It
 * never changes catalog state, derives scores, or treats missing data as a
 * healthy value. Signals are intentionally ordered from most urgent to least
 * urgent so the first visible item is always the most actionable one.
 */
export function buildMonitoringSignals(input: MonitoringInput): MonitoringSignal[] {
  const { data, health, healthError, healthLoading } = input;
  const signals: MonitoringSignal[] = [];

  if (healthError && !health) {
    signals.push({
      id: 'service-unreachable',
      severity: 'critical',
      title: 'Catalog API is unreachable',
      detail: 'The monitoring request did not receive a response from the standalone Catalog service.',
      action: 'retry',
    });
  } else if (health?.service.status === 'degraded') {
    signals.push({
      id: 'service-degraded',
      severity: 'critical',
      title: 'Catalog service is degraded',
      detail: health.service.databaseReadable
        ? 'The service is responding, but its catalog is not currently safe to report as healthy.'
        : 'The service is responding but cannot read its catalog database.',
      action: 'retry',
    });
  }

  if (data?.origin === 'snapshot' && !signals.some((signal) => signal.id === 'service-unreachable')) {
    signals.push({
      id: 'snapshot-active',
      severity: 'warning',
      title: 'Dashboard is using an offline snapshot',
      detail: 'Live provider data is unavailable. The snapshot remains visible but may be out of date.',
      action: 'retry',
    });
  }

  const staleIds = health?.catalog.staleProviders.map((provider) => provider.id)
    ?? data?.providers.filter((provider) => provider.freshness !== 'fresh').map((provider) => provider.id)
    ?? [];
  if (staleIds.length > 0) {
    const names = providerNames(data, staleIds);
    signals.push({
      id: 'stale-providers',
      severity: 'warning',
      title: `${staleIds.length} provider${staleIds.length === 1 ? '' : 's'} need a fresh sync`,
      detail: names ? `${names} has catalog data older than the freshness policy.` : 'Some provider data is older than the freshness policy.',
      action: 'retry',
    });
  }

  const failedSyncProviders = health?.lastSync?.providers.filter((provider) => provider.outcome !== 'ok') ?? [];
  if (failedSyncProviders.length > 0) {
    const names = failedSyncProviders.map((provider) => provider.provider).join(', ');
    signals.push({
      id: 'sync-failures',
      severity: 'warning',
      title: `${failedSyncProviders.length} provider sync${failedSyncProviders.length === 1 ? '' : 's'} reported an issue`,
      detail: names ? `${names} failed or was quarantined on the latest run.` : 'The latest sync reported a failed or quarantined provider.',
      action: 'changes',
    });
  }

  if (health?.service.syncInFlight) {
    signals.push({
      id: 'sync-in-flight',
      severity: 'info',
      title: 'Catalog sync is in progress',
      detail: health.service.currentRunStartedAt
        ? `A provider refresh started ${health.service.currentRunStartedAt}. Freshness will update when it completes.`
        : 'Provider refresh is currently running. Freshness will update when it completes.',
      action: 'none',
    });
  }

  const processingModels = data?.models.filter((model) => model.resolution.state === 'processing').length ?? 0;
  if (processingModels > 0) {
    signals.push({
      id: 'resolution-processing',
      severity: 'info',
      title: `${processingModels} model${processingModels === 1 ? '' : 's'} being resolved`,
      detail: 'The Catalog service is gathering additional evidence. No score is invented while resolution is in progress.',
      action: 'none',
    });
  }

  if (data?.meta.needsVerification && data.meta.needsVerification > 0) {
    signals.push({
      id: 'verification-needed',
      severity: 'info',
      title: `${data.meta.needsVerification} model${data.meta.needsVerification === 1 ? '' : 's'} need verification`,
      detail: 'They remain listed, but at least one operational fact is unresolved and should not be treated as complete.',
      action: 'none',
    });
  }

  if (signals.length === 0 && !healthLoading) {
    signals.push({
      id: 'all-clear',
      severity: 'success',
      title: 'Catalog monitoring is clear',
      detail: 'The service is responding and no freshness or synchronization issue is currently reported.',
      action: 'none',
    });
  }

  return signals;
}

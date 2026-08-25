import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import type { CatalogData, HealthResponse } from '../../api/client';

/**
 * The runtime summary moved here from the dashboard's settings card, so its
 * honesty contract moved with it: the sidebar states what /v1/health said and
 * renders absence as "not reported", never as a healthy-looking default.
 */
const catalogMock = vi.hoisted(() => ({ current: {
  data: null as CatalogData | null,
  health: null as HealthResponse | null,
  healthLoading: false,
} }));
vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => catalogMock.current,
}));

function health(over: Partial<HealthResponse['service']> = {}, catalog: Partial<HealthResponse['catalog']> = {}): HealthResponse {
  return {
    service: {
      status: 'up', databaseReadable: true, startedAt: null, syncInFlight: false,
      currentRunStartedAt: null, schedulerEnabled: true,
      nextScheduledRunAt: '2026-08-25T06:00:00.000Z',
      ...over,
    },
    catalog: { status: 'current', liveModels: 53, methodologyVersion: 'v2', staleAfterHours: 24, staleProviders: [], providers: [], ...catalog },
    lastSync: null,
  };
}

function renderSidebar() {
  return render(
    <MemoryRouter>
      <Sidebar open={false} onClose={() => {}} />
    </MemoryRouter>,
  );
}

describe('the sidebar runtime status', () => {
  test('summarises a healthy service with its next scheduled sync', () => {
    catalogMock.current = { data: null, health: health(), healthLoading: false };
    renderSidebar();

    expect(screen.getByText('Service up · catalog current')).toBeInTheDocument();
    expect(screen.getByText(/Next sync .+ · fresh < 24h/)).toBeInTheDocument();
  });

  test('a degraded service and a stale catalog are said out loud', () => {
    catalogMock.current = {
      data: null,
      health: health({ status: 'degraded', schedulerEnabled: false }, { status: 'stale' }),
      healthLoading: false,
    };
    renderSidebar();

    expect(screen.getByText('Service degraded · catalog stale')).toBeInTheDocument();
    expect(screen.getByText('Scheduler disabled')).toBeInTheDocument();
  });

  test('no health answer renders as not reported, never as a healthy default', () => {
    catalogMock.current = { data: null, health: null, healthLoading: false };
    renderSidebar();

    expect(screen.getByText('Service not reported')).toBeInTheDocument();
    expect(screen.queryByText(/Service up/)).not.toBeInTheDocument();
  });
});

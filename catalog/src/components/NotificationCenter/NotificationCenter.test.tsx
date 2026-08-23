import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchAlerts, updateAlertStatus } from '../../api/client';
import { ALERT_REFRESH_INTERVAL_MS, NotificationCenter } from './NotificationCenter';

vi.mock('../../api/client', () => ({
  fetchAlerts: vi.fn(),
  formatAgo: () => 'moments ago',
  updateAlertStatus: vi.fn(),
}));

const mockedFetchAlerts = vi.mocked(fetchAlerts);
const mockedUpdateAlertStatus = vi.mocked(updateAlertStatus);

function alertResponse(alerts: Awaited<ReturnType<typeof fetchAlerts>>['alerts']) {
  return {
    alerts,
    summary: { total: alerts.length, active: alerts.length, open: alerts.length, acknowledged: 0, resolved: 0, critical: 0, warning: alerts.length, info: 0 },
    generatedAt: '2026-08-23T09:00:00.000Z',
  };
}

function openAlert(overrides: Partial<Awaited<ReturnType<typeof fetchAlerts>>['alerts'][number]> = {}) {
  return {
    id: 'clinepass-stale',
    kind: 'provider_stale',
    severity: 'warning' as const,
    title: 'Clinepass sync is stale',
    detail: 'The provider roster needs a fresh successful sync.',
    providerId: 'clinepass',
    modelId: null,
    status: 'open' as const,
    firstSeenAt: '2026-08-23T08:00:00.000Z',
    lastSeenAt: '2026-08-23T09:00:00.000Z',
    acknowledgedAt: null,
    resolvedAt: null,
    occurrenceCount: 1,
    ...overrides,
  };
}

describe('NotificationCenter', () => {
  beforeEach(() => {
    mockedFetchAlerts.mockReset();
    mockedUpdateAlertStatus.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows open alerts for the active provider and acknowledges through the alert ledger', async () => {
    mockedFetchAlerts.mockResolvedValue(alertResponse([
      openAlert(),
      openAlert({ id: 'other-alert', title: 'Another provider sync is stale', detail: 'A different provider event.', providerId: 'another-provider', lastSeenAt: '2026-08-23T08:00:00.000Z' }),
    ]));
    mockedUpdateAlertStatus.mockResolvedValue({
      ...openAlert(),
      status: 'acknowledged',
      acknowledgedAt: '2026-08-23T09:01:00.000Z',
    });

    render(
      <MemoryRouter>
        <NotificationCenter providerId="clinepass" />
      </MemoryRouter>,
    );

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 1 open'));

    fireEvent.click(trigger);
    expect(await screen.findByText('Clinepass sync is stale')).toBeInTheDocument();
    expect(screen.queryByText('Another provider sync is stale')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Acknowledge Clinepass sync is stale' }));
    await waitFor(() => expect(mockedUpdateAlertStatus).toHaveBeenCalledWith('clinepass-stale', 'acknowledged'));
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications'));
  });

  it('closes on Escape and returns keyboard focus to the bell trigger', async () => {
    mockedFetchAlerts.mockResolvedValue(alertResponse([openAlert()]));
    render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 1 open'));
    fireEvent.click(trigger);
    expect(screen.getByRole('dialog', { name: 'Catalog notifications' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Catalog notifications' })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('keeps an alert visible and explains the failure when acknowledgement is rejected', async () => {
    mockedFetchAlerts.mockResolvedValue(alertResponse([openAlert()]));
    mockedUpdateAlertStatus.mockRejectedValue(new Error('Service unavailable'));
    render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 1 open'));
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('button', { name: 'Acknowledge Clinepass sync is stale' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('could not be acknowledged');
    expect(screen.getByText('Clinepass sync is stale')).toBeInTheDocument();
    expect(trigger).toHaveAccessibleName('Notifications, 1 open');
  });

  it('refreshes while visible, refreshes on focus, and stops polling after unmount', async () => {
    vi.useFakeTimers();
    mockedFetchAlerts.mockResolvedValue(alertResponse([openAlert()]));
    const { unmount } = render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    await act(async () => { await Promise.resolve(); });
    expect(mockedFetchAlerts).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(ALERT_REFRESH_INTERVAL_MS); });
    expect(mockedFetchAlerts).toHaveBeenCalledTimes(2);

    await act(async () => { window.dispatchEvent(new Event('focus')); await Promise.resolve(); });
    expect(mockedFetchAlerts).toHaveBeenCalledTimes(3);

    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(ALERT_REFRESH_INTERVAL_MS); });
    expect(mockedFetchAlerts).toHaveBeenCalledTimes(3);
  });
});

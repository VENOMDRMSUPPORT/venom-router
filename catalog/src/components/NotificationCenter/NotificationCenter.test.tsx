import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchAlerts, updateAlertStatus } from '../../api/client';
import { NotificationCenter } from './NotificationCenter';

vi.mock('../../api/client', () => ({
  fetchAlerts: vi.fn(),
  formatAgo: () => 'moments ago',
  updateAlertStatus: vi.fn(),
}));

const mockedFetchAlerts = vi.mocked(fetchAlerts);
const mockedUpdateAlertStatus = vi.mocked(updateAlertStatus);

describe('NotificationCenter', () => {
  beforeEach(() => {
    mockedFetchAlerts.mockReset();
    mockedUpdateAlertStatus.mockReset();
  });

  it('shows open alerts for the active provider and acknowledges through the alert ledger', async () => {
    mockedFetchAlerts.mockResolvedValue({
      alerts: [
        {
          id: 'clinepass-stale',
          kind: 'provider_stale',
          severity: 'warning',
          title: 'Clinepass sync is stale',
          detail: 'The provider roster needs a fresh successful sync.',
          providerId: 'clinepass',
          modelId: null,
          status: 'open',
          firstSeenAt: '2026-08-23T08:00:00.000Z',
          lastSeenAt: '2026-08-23T09:00:00.000Z',
          acknowledgedAt: null,
          resolvedAt: null,
          occurrenceCount: 1,
        },
        {
          id: 'other-alert',
          kind: 'provider_stale',
          severity: 'warning',
          title: 'Another provider sync is stale',
          detail: 'A different provider event.',
          providerId: 'another-provider',
          modelId: null,
          status: 'open',
          firstSeenAt: '2026-08-23T08:00:00.000Z',
          lastSeenAt: '2026-08-23T08:00:00.000Z',
          acknowledgedAt: null,
          resolvedAt: null,
          occurrenceCount: 1,
        },
      ],
      summary: { total: 2, active: 2, open: 2, acknowledged: 0, resolved: 0, critical: 0, warning: 2, info: 0 },
      generatedAt: '2026-08-23T09:00:00.000Z',
    });
    mockedUpdateAlertStatus.mockResolvedValue({
      id: 'clinepass-stale',
      kind: 'provider_stale',
      severity: 'warning',
      title: 'Clinepass sync is stale',
      detail: 'The provider roster needs a fresh successful sync.',
      providerId: 'clinepass',
      modelId: null,
      status: 'acknowledged',
      firstSeenAt: '2026-08-23T08:00:00.000Z',
      lastSeenAt: '2026-08-23T09:00:00.000Z',
      acknowledgedAt: '2026-08-23T09:01:00.000Z',
      resolvedAt: null,
      occurrenceCount: 1,
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
});

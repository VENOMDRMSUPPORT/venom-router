import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchCatalogNotifications, markCatalogNotificationsRead } from '../../api/client';
import { NOTIFICATION_REFRESH_INTERVAL_MS, NotificationCenter } from './NotificationCenter';

vi.mock('../../api/client', () => ({
  fetchCatalogNotifications: vi.fn(),
  formatAgo: () => 'moments ago',
  markCatalogNotificationsRead: vi.fn(),
}));

const mockedFetchNotifications = vi.mocked(fetchCatalogNotifications);
const mockedMarkRead = vi.mocked(markCatalogNotificationsRead);

function notificationResponse(
  notifications: Awaited<ReturnType<typeof fetchCatalogNotifications>>['notifications'],
  summaryOverride?: { total: number; unread: number; read: number },
) {
  return {
    notifications,
    summary: summaryOverride ?? { total: notifications.length, unread: notifications.filter((notification) => notification.readAt === null).length, read: notifications.filter((notification) => notification.readAt !== null).length },
    generatedAt: '2026-08-23T09:00:00.000Z',
  };
}

function notification(overrides: Partial<Awaited<ReturnType<typeof fetchCatalogNotifications>>['notifications'][number]> = {}) {
  return {
    id: 'model-event:101',
    category: 'success' as const,
    kind: 'model_added' as const,
    title: 'clinepass added model clinepass-code',
    detail: 'The provider added this model to its published roster.',
    providerId: 'clinepass',
    modelId: 'clinepass-code',
    observedAt: '2026-08-23T09:00:00.000Z',
    readAt: null,
    createdAt: '2026-08-23T09:00:00.000Z',
    ...overrides,
  };
}

describe('NotificationCenter', () => {
  beforeEach(() => {
    mockedFetchNotifications.mockReset();
    mockedMarkRead.mockReset();
  });

  afterEach(() => vi.useRealTimers());

  it('renders model-event history and marks unread notifications read without acknowledgement workflow', async () => {
    mockedFetchNotifications.mockResolvedValue(notificationResponse([notification()]));
    mockedMarkRead.mockResolvedValue({ updated: 1 });
    render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 1 unread'));
    expect(mockedFetchNotifications).toHaveBeenCalledWith('clinepass', expect.any(AbortSignal));

    fireEvent.click(trigger);
    expect(await screen.findByText('clinepass added model clinepass-code')).toBeInTheDocument();
    expect(screen.queryByText(/Operational alerts/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Mark all as read' }));

    await waitFor(() => expect(mockedMarkRead).toHaveBeenCalledWith(null, 'clinepass'));
    expect(trigger).toHaveAccessibleName('Notifications');
  });

  it('says how many it could not show when the service page is shorter than the total', async () => {
    // The bell counts every unread row; the panel can only show one page. When
    // those two numbers differ the difference used to be invisible — 135 unread
    // over a panel of 5. Above the service ceiling it is stated instead.
    const notifications = Array.from({ length: 3 }, (_, index) => notification({ id: `model-event:${200 + index}` }));
    mockedFetchNotifications.mockResolvedValue(notificationResponse(notifications, { total: 9, unread: 9, read: 0 }));
    render(<MemoryRouter><NotificationCenter /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 9 unread'));
    fireEvent.click(trigger);

    expect(screen.getByText('Showing the 3 most recent of 9.')).toBeInTheDocument();
  });

  it('stays silent when the page already holds everything', async () => {
    const notifications = Array.from({ length: 3 }, (_, index) => notification({ id: `model-event:${300 + index}` }));
    mockedFetchNotifications.mockResolvedValue(notificationResponse(notifications, { total: 3, unread: 3, read: 0 }));
    render(<MemoryRouter><NotificationCenter /></MemoryRouter>);

    fireEvent.click(await screen.findByRole('button', { name: 'Notifications, 3 unread' }));

    expect(screen.queryByText(/most recent of/)).not.toBeInTheDocument();
  });

  it('renders every fetched notification in the scrollable history and marks the full scope read', async () => {
    const notifications = Array.from({ length: 30 }, (_, index) => notification({
      id: `model-event:${101 + index}`,
      title: `clinepass added model clinepass-code-${index}`,
      modelId: `clinepass-code-${index}`,
    }));
    mockedFetchNotifications.mockResolvedValue(notificationResponse(notifications));
    mockedMarkRead.mockResolvedValue({ updated: 30 });
    render(<MemoryRouter><NotificationCenter /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 30 unread'));
    fireEvent.click(trigger);

    expect(screen.getAllByRole('listitem')).toHaveLength(30);
    expect(screen.getByText('clinepass added model clinepass-code-29')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Mark all as read' }));

    await waitFor(() => expect(mockedMarkRead).toHaveBeenCalledWith(null, undefined));
    expect(trigger).toHaveAccessibleName('Notifications');
  });

  it('keeps history visible and reports a failed read update', async () => {
    mockedFetchNotifications.mockResolvedValue(notificationResponse([notification()]));
    mockedMarkRead.mockRejectedValue(new Error('Service unavailable'));
    render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 1 unread'));
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('button', { name: 'Mark all as read' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('could not be marked as read');
    expect(screen.getByText('clinepass added model clinepass-code')).toBeInTheDocument();
    expect(trigger).toHaveAccessibleName('Notifications, 1 unread');
  });

  it('closes on Escape and returns keyboard focus to the bell trigger', async () => {
    mockedFetchNotifications.mockResolvedValue(notificationResponse([notification()]));
    render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 1 unread'));
    fireEvent.click(trigger);
    expect(screen.getByRole('dialog', { name: 'Catalog notifications' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Catalog notifications' })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('refreshes while visible, refreshes on focus, and stops polling after unmount', async () => {
    vi.useFakeTimers();
    mockedFetchNotifications.mockResolvedValue(notificationResponse([notification()]));
    const { unmount } = render(<MemoryRouter><NotificationCenter providerId="clinepass" /></MemoryRouter>);

    await act(async () => { await Promise.resolve(); });
    expect(mockedFetchNotifications).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(NOTIFICATION_REFRESH_INTERVAL_MS); });
    expect(mockedFetchNotifications).toHaveBeenCalledTimes(2);
    await act(async () => { window.dispatchEvent(new Event('focus')); await Promise.resolve(); });
    expect(mockedFetchNotifications).toHaveBeenCalledTimes(3);
    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(NOTIFICATION_REFRESH_INTERVAL_MS); });
    expect(mockedFetchNotifications).toHaveBeenCalledTimes(3);
  });
});

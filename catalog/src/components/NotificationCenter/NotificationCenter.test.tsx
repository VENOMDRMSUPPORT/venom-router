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

  it('uses the server unread total and global scope even when only a limited list is rendered', async () => {
    mockedFetchNotifications.mockResolvedValue(notificationResponse([notification()], { total: 105, unread: 105, read: 0 }));
    mockedMarkRead.mockResolvedValue({ updated: 105 });
    render(<MemoryRouter><NotificationCenter /></MemoryRouter>);

    const trigger = screen.getByRole('button', { name: 'Notifications' });
    await waitFor(() => expect(trigger).toHaveAccessibleName('Notifications, 105 unread'));
    fireEvent.click(trigger);
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

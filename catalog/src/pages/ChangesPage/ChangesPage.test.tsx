import { act, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchChanges } from '../../api/client';
import { ChangesPage } from './ChangesPage';

vi.mock('../../api/client', () => ({
  fetchChanges: vi.fn(),
  formatAgo: () => 'moments ago',
}));

const mockedFetchChanges = vi.mocked(fetchChanges);

function change(overrides: Record<string, unknown> = {}) {
  return {
    class: 'added',
    providerId: 'opencode-zen',
    modelId: 'x-preview-f-free',
    field: null,
    from: null,
    to: null,
    note: null,
    observedAt: '2026-08-23T09:00:00.000Z',
    ...overrides,
  } as unknown as Awaited<ReturnType<typeof fetchChanges>>['changes'][number];
}

const changesResponse = (changes: ReturnType<typeof change>[]) =>
  ({ changes, byClass: {}, cursor: '2026-08-23T09:00:00.000Z' }) as Awaited<ReturnType<typeof fetchChanges>>;

const renderPage = () => render(<MemoryRouter><ChangesPage /></MemoryRouter>);

describe('ChangesPage', () => {
  beforeEach(() => {
    mockedFetchChanges.mockResolvedValue(changesResponse([change()]));
  });

  afterEach(() => vi.clearAllMocks());

  it('passes an abort signal, so unmounting cancels the request it started', async () => {
    const view = renderPage();
    await act(async () => { await Promise.resolve(); });

    const signal = mockedFetchChanges.mock.calls[0][1];
    expect(signal).toBeInstanceOf(AbortSignal);
    expect(signal!.aborted).toBe(false);

    view.unmount();
    expect(signal!.aborted).toBe(true);
  });

  it('an aborted fetch is not rendered as a failure', async () => {
    // React StrictMode mounts twice in development, so the first request is
    // always aborted by the cleanup. Reported as an error, that painted
    // "The operation was aborted" over a page that was fine.
    const aborted = Object.assign(new Error('The operation was aborted.'), { name: 'AbortError' });
    mockedFetchChanges.mockImplementation((_since, signal) => {
      // Abort exactly as a cleanup would, then reject the way fetch does.
      Object.defineProperty(signal!, 'aborted', { value: true, configurable: true });
      return Promise.reject(aborted);
    });

    renderPage();
    await act(async () => { await Promise.resolve(); });

    expect(screen.queryByText(/aborted/i)).toBeNull();
  });

  it('a genuine failure is still reported', async () => {
    mockedFetchChanges.mockRejectedValue(new Error('catalog service unreachable'));

    renderPage();
    await act(async () => { await Promise.resolve(); });

    expect(screen.getByText(/catalog service unreachable/i)).toBeTruthy();
  });
});

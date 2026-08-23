import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchCatalog, fetchChangeCursor, fetchHealth } from '../api/client';
import { CHANGE_CURSOR_POLL_MS, CatalogProvider, useCatalog } from './useCatalog';

vi.mock('../api/client', () => ({
  fetchCatalog: vi.fn(),
  fetchChangeCursor: vi.fn(),
  fetchHealth: vi.fn(),
}));

const mockedFetchCatalog = vi.mocked(fetchCatalog);
const mockedFetchChangeCursor = vi.mocked(fetchChangeCursor);
const mockedFetchHealth = vi.mocked(fetchHealth);

function catalog(liveModels: string[]) {
  return {
    models: liveModels.map((modelId) => ({
      providerId: 'acme',
      modelId,
      resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    })),
    providers: [],
    meta: null,
    origin: 'live',
  } as unknown as Awaited<ReturnType<typeof fetchCatalog>>;
}

function snapshot(models: string[]) {
  return { ...catalog(models), origin: 'snapshot' as const };
}

/** Renders the model ids the context is currently serving, plus its revision. */
function Probe() {
  const { data, revision, loading } = useCatalog();
  return (
    <div>
      <span data-testid="models">{(data?.models ?? []).map((model) => model.modelId).join(',')}</span>
      <span data-testid="revision">{revision}</span>
      <span data-testid="loading">{String(loading)}</span>
    </div>
  );
}

const renderProbe = () => render(<CatalogProvider><Probe /></CatalogProvider>);

/** Let the mocked promises settle inside act, so React state lands before asserting. */
const settle = async () => { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); };

describe('CatalogProvider change-cursor polling', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockedFetchHealth.mockResolvedValue({} as unknown as Awaited<ReturnType<typeof fetchHealth>>);
    mockedFetchCatalog.mockResolvedValue(catalog(['model-a']));
    mockedFetchChangeCursor.mockResolvedValue('2026-08-23T09:00:00.000Z');
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('reads the cursor on mount without refetching the catalog for it', async () => {
    renderProbe();
    await settle();

    // The first reading only arms the comparison. Treating it as a change would
    // make every mount fetch the whole catalog twice.
    expect(mockedFetchChangeCursor).toHaveBeenCalledTimes(1);
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('revision').textContent).toBe('0');
  });

  it('refetches the catalog when the cursor moves, and shows the new roster', async () => {
    renderProbe();
    await settle();
    expect(screen.getByTestId('models').textContent).toBe('model-a');

    // A sync added a model: the event table's newest timestamp moved.
    mockedFetchCatalog.mockResolvedValue(catalog(['model-a', 'model-b']));
    mockedFetchChangeCursor.mockResolvedValue('2026-08-23T09:30:00.000Z');
    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();

    // Asserted after settling rather than through `waitFor`: that helper polls on
    // real timers, which fake timers have stopped, so it can only time out here.
    await settle();
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('revision').textContent).toBe('1');
    expect(screen.getByTestId('models').textContent).toBe('model-a,model-b');
  });

  it('does not refetch the catalog while the cursor is unchanged', async () => {
    renderProbe();
    await settle();

    // Three polls, same cursor. Polling the cursor instead of the catalog is the
    // whole point: an unchanged catalog must cost one aggregate, not a payload.
    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS * 3); });
    await settle();

    expect(mockedFetchChangeCursor.mock.calls.length).toBeGreaterThanOrEqual(4);
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('revision').textContent).toBe('0');
  });

  it('skips the poll while the tab is hidden and catches up when it returns', async () => {
    renderProbe();
    await settle();
    const afterMount = mockedFetchChangeCursor.mock.calls.length;

    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS * 2); });
    expect(mockedFetchChangeCursor.mock.calls.length).toBe(afterMount);

    visibility.mockReturnValue('visible');
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await settle();
    expect(mockedFetchChangeCursor.mock.calls.length).toBeGreaterThan(afterMount);
    visibility.mockRestore();
  });

  it('stops polling after unmount', async () => {
    const view = renderProbe();
    await settle();

    view.unmount();
    const afterUnmount = mockedFetchChangeCursor.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS * 2); });

    expect(mockedFetchChangeCursor.mock.calls.length).toBe(afterUnmount);
  });

  it('a failed probe leaves the loaded catalog standing and raises no error of its own', async () => {
    renderProbe();
    await settle();

    // The catalog fetch and the health poll already report an unreachable
    // service; a third voice saying it would only crowd the page.
    mockedFetchChangeCursor.mockRejectedValue(new Error('service unreachable'));
    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();

    expect(screen.getByTestId('models').textContent).toBe('model-a');
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(1);
  });

  it('keeps the rendered roster mounted while a cursor-triggered background refresh is pending', async () => {
    renderProbe();
    await settle();
    let resolveRefresh: ((value: Awaited<ReturnType<typeof fetchCatalog>>) => void) | undefined;
    mockedFetchCatalog.mockImplementationOnce(() => new Promise((resolve) => { resolveRefresh = resolve; }));
    mockedFetchChangeCursor.mockResolvedValue('2026-08-23T09:30:00.000Z');

    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();
    expect(screen.getByTestId('models').textContent).toBe('model-a');
    expect(screen.getByTestId('loading').textContent).toBe('false');

    await act(async () => { resolveRefresh?.(catalog(['model-a', 'model-b'])); await Promise.resolve(); });
    expect(screen.getByTestId('models').textContent).toBe('model-a,model-b');
  });

  it('retries the same changed cursor after its first refetch fails instead of committing it early', async () => {
    renderProbe();
    await settle();
    mockedFetchCatalog.mockRejectedValueOnce(new Error('temporary failure'));
    mockedFetchCatalog.mockResolvedValue(catalog(['model-a', 'model-b']));
    mockedFetchChangeCursor.mockResolvedValue('2026-08-23T09:30:00.000Z');

    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();
    expect(screen.getByTestId('models').textContent).toBe('model-a');
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(2);

    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(3);
    expect(screen.getByTestId('models').textContent).toBe('model-a,model-b');
  });

  it('keeps a changed cursor pending when its refetch falls back to a stale snapshot', async () => {
    renderProbe();
    await settle();
    mockedFetchCatalog.mockResolvedValueOnce(snapshot(['snapshot-only']));
    mockedFetchCatalog.mockResolvedValueOnce(catalog(['model-a', 'model-b']));
    mockedFetchChangeCursor.mockResolvedValue('2026-08-23T09:30:00.000Z');

    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('models').textContent).toBe('model-a');

    // The service still reports T2. Because the snapshot did not commit it, the
    // provider retries the same cursor and only marks it rendered after live data.
    await act(async () => { await vi.advanceTimersByTimeAsync(CHANGE_CURSOR_POLL_MS); });
    await settle();
    expect(mockedFetchCatalog).toHaveBeenCalledTimes(3);
    expect(screen.getByTestId('models').textContent).toBe('model-a,model-b');
  });
});

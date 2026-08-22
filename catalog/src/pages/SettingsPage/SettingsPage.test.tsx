import { beforeEach, describe, expect, test, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SettingsPage } from './SettingsPage';

const provider = (id: string, freshness: string) => ({
  id, name: id, rosterUrl: '', liveModels: 3, lastSuccessfulSyncAt: '2026-08-21T00:00:00.000Z',
  lastAttemptedSyncAt: '2026-08-21T00:00:00.000Z', lastOutcome: 'ok', freshness, hoursSinceSuccess: 1,
  qualityScored: 3, modelScoreScored: 3, overallScoreScored: 3, unrated: 0,
});

const providers: ReturnType<typeof provider>[] = [];

vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({
    data: {
      providers,
      meta: { liveModels: 65 },
    },
    loading: false,
    error: null,
    reload: vi.fn(),
  }),
}));

function renderSettings() {
  return render(
    <MemoryRouter>
      <SettingsPage />
    </MemoryRouter>,
  );
}

describe('SettingsPage', () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-reduce-motion');
  });

  test('saves the selected default provider view', () => {
    renderSettings();

    fireEvent.click(screen.getByRole('button', { name: /grid by default/i }));

    expect(JSON.parse(window.localStorage.getItem('venom-catalog-settings') ?? '{}')).toMatchObject({
      defaultView: 'grid',
    });
  });

  test('keeps the change activity feed reachable', () => {
    renderSettings();

    expect(screen.getByRole('link', { name: /open what’s new/i })).toHaveAttribute('href', '/changes');
    expect(screen.getByText('65')).toBeInTheDocument();
  });

  /**
   * The tile counted stale providers and nothing else while calling itself
   * "Needs attention", so it read 0 on a catalog where the dashboard's own tiles
   * reported 40 models carrying an unresolved source conflict and one missing a
   * required fact. Two surfaces in one app disagreeing about whether anything is
   * wrong is worse than either number alone; the health tile has to name the one
   * thing it measures.
   */
  test('the staleness tile says what it counts', () => {
    providers.length = 0;
    providers.push(provider('a', 'fresh'), provider('b', 'stale'), provider('c', 'stale'));

    renderSettings();

    const tile = screen.getByText('Stale providers').closest('div');
    expect(tile).not.toBeNull();
    expect(tile).toHaveTextContent('2');
    expect(screen.queryByText('Needs attention')).toBeNull();
  });
});

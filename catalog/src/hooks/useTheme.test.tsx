import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ThemeProvider, useTheme } from './useTheme';

function Probe() {
  const { theme, toggleTheme, reduceMotion, setReduceMotion } = useTheme();
  return (
    <div>
      <output data-testid="theme">{theme}</output>
      <output data-testid="reduce-motion">{String(reduceMotion)}</output>
      <button type="button" onClick={toggleTheme}>Toggle theme</button>
      <button type="button" onClick={() => setReduceMotion(true)}>Enable reduced motion</button>
    </div>
  );
}

function renderTheme() {
  return render(<ThemeProvider><Probe /></ThemeProvider>);
}

function mockMotionPreference(matches: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  let currentMatches = matches;
  const mediaQuery = {
    get matches() { return currentMatches; },
    media: '(prefers-reduced-motion: reduce)',
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
    dispatch: (nextMatches: boolean) => {
      currentMatches = nextMatches;
      for (const listener of listeners) listener({ matches: nextMatches } as MediaQueryListEvent);
    },
  } as unknown as MediaQueryList & { dispatch: (matches: boolean) => void };
  const matchMedia = vi.isMockFunction(window.matchMedia)
    ? vi.mocked(window.matchMedia)
    : vi.spyOn(window, 'matchMedia');
  matchMedia.mockReturnValue(mediaQuery);
  return mediaQuery;
}

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme-changing');
  document.documentElement.removeAttribute('data-reduce-motion');
  document.documentElement.removeAttribute('data-theme');
  mockMotionPreference(false);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ThemeProvider', () => {
  it('defaults to dark when the theme is missing', () => {
    renderTheme();
    expect(screen.getByTestId('theme')).toHaveTextContent('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(document.documentElement.style.colorScheme).toBe('dark');
  });

  it('uses a valid light value from direct storage', () => {
    window.localStorage.setItem('catalog-theme', 'light');
    renderTheme();
    expect(screen.getByTestId('theme')).toHaveTextContent('light');
    expect(document.documentElement.dataset.theme).toBe('light');
  });

  it('falls back to dark for an invalid direct storage value', () => {
    window.localStorage.setItem('catalog-theme', 'neon');
    window.localStorage.setItem('venom-catalog-settings', JSON.stringify({ theme: 'light' }));
    renderTheme();
    expect(screen.getByTestId('theme')).toHaveTextContent('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
  });

  it('reads a valid legacy settings theme when direct theme storage is absent', () => {
    window.localStorage.setItem('venom-catalog-settings', JSON.stringify({ theme: 'light' }));
    renderTheme();
    expect(screen.getByTestId('theme')).toHaveTextContent('light');
  });

  it('toggles the document, persists the choice, and updates the accessible source state', () => {
    renderTheme();
    fireEvent.click(screen.getByRole('button', { name: 'Toggle theme' }));
    expect(screen.getByTestId('theme')).toHaveTextContent('light');
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(document.documentElement.style.colorScheme).toBe('light');
    expect(window.localStorage.getItem('catalog-theme')).toBe('light');
  });

  it('applies storage events without writing back or creating a loop', () => {
    renderTheme();
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    act(() => window.dispatchEvent(new StorageEvent('storage', { key: 'catalog-theme', newValue: 'light' })));
    expect(screen.getByTestId('theme')).toHaveTextContent('light');
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(setItem).not.toHaveBeenCalled();
  });

  it('handles malformed storage and storage failures without crashing', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => '{bad json');
    const malformedView = renderTheme();
    expect(screen.getByTestId('theme')).toHaveTextContent('dark');
    malformedView.unmount();

    vi.restoreAllMocks();
    mockMotionPreference(false);
    renderTheme();
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('storage denied'); });
    expect(() => fireEvent.click(screen.getByRole('button', { name: 'Toggle theme' }))).not.toThrow();
    expect(screen.getByTestId('theme')).toHaveTextContent('light');
  });

  it('combines the explicit reduce-motion choice with system preference', () => {
    const mediaQuery = mockMotionPreference(true);
    renderTheme();
    expect(screen.getByTestId('reduce-motion')).toHaveTextContent('false');
    expect(document.documentElement.dataset.reduceMotion).toBe('true');

    fireEvent.click(screen.getByRole('button', { name: 'Enable reduced motion' }));
    expect(screen.getByTestId('reduce-motion')).toHaveTextContent('true');
    expect(window.localStorage.getItem('venom-catalog-settings')).toContain('"reduceMotion":true');

    act(() => mediaQuery.dispatch(false));
    expect(document.documentElement.dataset.reduceMotion).toBe('true');
  });

  it('responds to a settings storage event for reduce motion', () => {
    renderTheme();
    act(() => window.dispatchEvent(new StorageEvent('storage', {
      key: 'venom-catalog-settings',
      newValue: JSON.stringify({ reduceMotion: true }),
    })));
    expect(screen.getByTestId('reduce-motion')).toHaveTextContent('true');
    expect(document.documentElement.dataset.reduceMotion).toBe('true');
  });

  it('guards rapid toggles with one current transition blocker and cleans it on unmount', () => {
    const requestAnimationFrame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback: FrameRequestCallback) => {
      return window.setTimeout(() => callback(performance.now()), 0);
    });
    const cancelAnimationFrame = vi.spyOn(window, 'cancelAnimationFrame');
    const view = renderTheme();
    fireEvent.click(screen.getByRole('button', { name: 'Toggle theme' }));
    fireEvent.click(screen.getByRole('button', { name: 'Toggle theme' }));
    fireEvent.click(screen.getByRole('button', { name: 'Toggle theme' }));
    expect(requestAnimationFrame).toHaveBeenCalled();
    expect(document.querySelectorAll('style')).toHaveLength(0);
    expect(document.documentElement.dataset.themeChanging).toBe('true');
    view.unmount();
    expect(cancelAnimationFrame).toHaveBeenCalled();
    expect(document.documentElement.dataset.themeChanging).toBeUndefined();
  });

  it('removes storage and media listeners on unmount', () => {
    const addEventListener = vi.spyOn(window, 'addEventListener');
    const removeEventListener = vi.spyOn(window, 'removeEventListener');
    const mediaQuery = mockMotionPreference(false);
    const view = renderTheme();
    view.unmount();
    expect(addEventListener).toHaveBeenCalledWith('storage', expect.any(Function));
    expect(removeEventListener).toHaveBeenCalledWith('storage', expect.any(Function));
    expect(mediaQuery.removeEventListener).toBeDefined();
  });
});

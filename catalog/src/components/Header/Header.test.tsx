import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Header } from './Header';
import { ThemeProvider, useTheme } from '../../hooks/useTheme';
import { SettingsPage } from '../../pages/SettingsPage/SettingsPage';

vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({
    data: { providers: [], meta: { liveModels: 0 } },
    loading: false,
    error: null,
    reload: vi.fn(),
  }),
}));

vi.mock('../NotificationCenter/NotificationCenter', () => ({
  NotificationCenter: () => <button type="button" aria-label="Notifications">Notifications</button>,
}));

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function SharedThemeTree() {
  const { theme, toggleTheme } = useTheme();
  return (
    <>
      <Header theme={theme} onToggleTheme={toggleTheme} />
      <SettingsPage />
      <LocationProbe />
    </>
  );
}

function renderSharedTree() {
  return render(
    <MemoryRouter initialEntries={['/settings']}>
      <ThemeProvider>
        <SharedThemeTree />
      </ThemeProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme-changing');
});

describe('Header and Settings theme synchronization', () => {
  it('updates Settings immediately when Header toggles and preserves route/data context', () => {
    renderSharedTree();
    expect(screen.getByTestId('location')).toHaveTextContent('/settings');
    expect(screen.getByRole('button', { name: 'Switch to light theme' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Light Brighter canvas/i })).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(screen.getByRole('button', { name: 'Switch to light theme' }));
    expect(screen.getByRole('button', { name: 'Switch to dark theme' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Light Brighter canvas/i })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('location')).toHaveTextContent('/settings');
  });

  it('updates Header immediately when Settings changes and persists the shared value', () => {
    renderSharedTree();
    fireEvent.click(screen.getByRole('button', { name: /Light Brighter canvas/i }));
    expect(screen.getByRole('button', { name: 'Switch to dark theme' })).toBeInTheDocument();
    expect(window.localStorage.getItem('catalog-theme')).toBe('light');
    expect(document.documentElement.dataset.theme).toBe('light');
  });
});

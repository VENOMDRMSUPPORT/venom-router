import { beforeEach, describe, expect, test, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SettingsPage } from './SettingsPage';

vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({
    data: {
      providers: [],
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
});

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, test } from 'vitest';
import App from './App';
import { ThemeProvider } from './hooks/useTheme';

function renderDocs(path = '/docs/') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ThemeProvider>
        <App />
      </ThemeProvider>
    </MemoryRouter>,
  );
}

describe('embedded documentation route', () => {
  test('renders the docs shell without the dashboard shell', () => {
    renderDocs();
    expect(screen.getByRole('heading', { name: 'Venom Catalog' })).toBeInTheDocument();
    expect(screen.getByText('Public documentation for the current local Catalog boundary.')).toBeInTheDocument();
    expect(document.querySelector('.app-layout')).toBeNull();
    expect(document.querySelector('.docs-sidebar')).toBeInTheDocument();
  });

  test('keeps internal documentation links under the docs prefix', () => {
    renderDocs();
    expect(screen.getAllByRole('link', { name: 'Quick Start' }).every((link) => link.getAttribute('href') === '/docs/guides/quick-start')).toBe(true);
    expect(screen.getAllByRole('link', { name: 'API Overview' }).every((link) => link.getAttribute('href') === '/docs/api/overview')).toBe(true);
  });
});

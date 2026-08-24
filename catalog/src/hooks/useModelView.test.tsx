import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { SETTINGS_STORAGE_KEY } from './useTheme';
import { useModelView } from './useModelView';

function Probe() {
  const [view, setView] = useModelView();
  return (
    <div>
      <output data-testid="view">{view}</output>
      <button type="button" onClick={() => setView('table')}>Table view</button>
      <button type="button" onClick={() => setView('grid')}>Grid view</button>
    </div>
  );
}

const originalWidth = window.innerWidth;

function setViewportWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true, writable: true });
}

beforeEach(() => {
  window.localStorage.clear();
  setViewportWidth(1280);
});

afterEach(() => {
  window.localStorage.clear();
  setViewportWidth(originalWidth);
});

describe('the view a page listing models opens in', () => {
  it('opens a desktop reader with no saved preference in the table', () => {
    render(<Probe />);
    expect(screen.getByTestId('view')).toHaveTextContent('table');
  });

  it('opens a narrow reader with no saved preference in the grid', () => {
    setViewportWidth(420);
    render(<Probe />);
    expect(screen.getByTestId('view')).toHaveTextContent('grid');
  });

  it('lets the saved Settings default outrank the viewport', () => {
    setViewportWidth(420);
    window.localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify({ defaultView: 'table' }));
    render(<Probe />);
    expect(screen.getByTestId('view')).toHaveTextContent('table');
  });

  it('falls back to the viewport when the stored preference is malformed', () => {
    setViewportWidth(420);
    window.localStorage.setItem(SETTINGS_STORAGE_KEY, '{ not json');
    render(<Probe />);
    expect(screen.getByTestId('view')).toHaveTextContent('grid');

    window.localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify({ defaultView: 'carousel' }));
    render(<Probe />);
    expect(screen.getAllByTestId('view')[1]).toHaveTextContent('grid');
  });

  /**
   * The regression the removed `resize` listener produced: the dashboard
   * re-applied the viewport rule on every resize event, so narrowing the window
   * moved a reader off the view they had just chosen while the switcher still
   * reported their choice as active. The viewport picks the opening view; it
   * does not overrule the reader afterwards.
   */
  it('keeps an explicit choice when the viewport narrows underneath it', () => {
    render(<Probe />);
    expect(screen.getByTestId('view')).toHaveTextContent('table');

    setViewportWidth(420);
    act(() => { fireEvent(window, new Event('resize')); });

    expect(screen.getByTestId('view')).toHaveTextContent('table');
  });

  it('follows an explicit switch in either direction', () => {
    render(<Probe />);

    fireEvent.click(screen.getByRole('button', { name: 'Grid view' }));
    expect(screen.getByTestId('view')).toHaveTextContent('grid');

    fireEvent.click(screen.getByRole('button', { name: 'Table view' }));
    expect(screen.getByTestId('view')).toHaveTextContent('table');
  });
});

import { describe, test, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { Toolbar, MODEL_FILTERS } from './Toolbar';

function renderToolbar(props: Partial<Parameters<typeof Toolbar>[0]> = {}) {
  const onFilterChange = vi.fn();
  render(
    <Toolbar
      query=""
      onQueryChange={vi.fn()}
      filter="all"
      onFilterChange={onFilterChange}
      view="table"
      onViewChange={vi.fn()}
      {...props}
    />,
  );
  // The trigger is named by the active option, which is how a reader knows what
  // is filtering the page without opening the menu.
  const open = (name: RegExp | string) => fireEvent.click(screen.getByRole('button', { name }));
  return { onFilterChange, open };
}

const optionLabels = () => screen.getAllByRole('option').map((el) => el.textContent);

describe('Toolbar', () => {
  test('offers the model filters by default', () => {
    const { open } = renderToolbar();

    open(/all models/i);

    expect(optionLabels()).toEqual(MODEL_FILTERS.map((o) => o.label));
  });

  /**
   * One hardcoded option list served three pages, so "What's new" — which
   * filters by CHANGE CLASS — offered "Free Models" and "1M+ Context". Picking
   * either compared a change class against a model predicate, matched nothing,
   * and emptied the page. A filter that can only silently hide everything is
   * worse than no filter, so each page declares the filters it actually has.
   */
  test('a caller can declare its own filters', () => {
    const { open } = renderToolbar({
      options: [
        { value: 'all', label: 'All Events' },
        { value: 'price_changed', label: 'Price' },
      ],
    });

    open(/all events/i);

    expect(optionLabels()).toEqual(['All Events', 'Price']);
  });

  test('the trigger names the active filter from the caller list, not the default one', () => {
    renderToolbar({
      filter: 'price_changed',
      options: [
        { value: 'all', label: 'All Events' },
        { value: 'price_changed', label: 'Price' },
      ],
    });

    expect(screen.getByRole('button', { name: /price/i })).toBeInTheDocument();
  });

  test('an active filter the caller does not offer falls back to its first option', () => {
    // Guards the page-switch case: the filter state is a plain string, so a
    // value left over from another page must not leave the trigger blank.
    renderToolbar({
      filter: 'free',
      options: [
        { value: 'all', label: 'All Events' },
        { value: 'price_changed', label: 'Price' },
      ],
    });

    expect(screen.getByRole('button', { name: /all events/i })).toBeInTheDocument();
  });

  test('choosing an option reports its value', () => {
    const { onFilterChange, open } = renderToolbar();

    open(/all models/i);
    fireEvent.click(screen.getByRole('option', { name: 'Free Models' }));

    expect(onFilterChange).toHaveBeenCalledWith('free');
  });
});

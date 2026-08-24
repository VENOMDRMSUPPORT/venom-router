import { useState } from 'react';
import { readStoredDefaultView } from './useTheme';

export type ModelView = 'grid' | 'table';

/**
 * Below this width the model table cannot show its columns without scrolling
 * sideways, so a reader who arrives on a phone and expressed no preference
 * opens in Grid. It is the same threshold the stylesheets use to turn the table
 * wrapper into a touch-scrollable region, and the two must not drift apart.
 */
export const NARROW_VIEWPORT_MAX_WIDTH = 768;

function viewportSuggestsGrid(): boolean {
  if (typeof window === 'undefined') return false;
  return window.innerWidth < NARROW_VIEWPORT_MAX_WIDTH;
}

/**
 * The single rule for which view a page listing models opens in.
 *
 * Precedence is explicit choice, then the saved Settings default, then the
 * viewport at first render. The viewport supplies a *default*, never an
 * override: it used to be re-applied from a `resize` listener on the dashboard,
 * which meant a reader who chose Table and then narrowed the window was moved
 * back to Grid while the switcher still reported Table as active. The table has
 * carried a horizontal scroll region at that width all along, so there was
 * nothing to rescue them from.
 *
 * Both pages that list models call this, so "which view do I open in" has one
 * answer instead of two that disagreed — the dashboard ignored the Settings
 * preference entirely, and the provider page ignored the viewport after mount.
 */
export function useModelView(): [ModelView, (view: ModelView) => void] {
  return useState<ModelView>(() => readStoredDefaultView() ?? (viewportSuggestsGrid() ? 'grid' : 'table'));
}

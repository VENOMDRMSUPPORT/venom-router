/**
 * Shared filter predicates for models and providers.
 *
 * Provides a single source of truth for filtering logic across the dashboard,
 * provider detail pages, and toolbar controls.
 */
import type { ApiModel } from './client';

/**
 * Checks whether a single model matches the selected filter option.
 */
export function modelMatchesFilter(m: ApiModel, filter: string): boolean {
  if (filter === 'all') return true;
  if (filter === 'free') return m.pricing.isFree === true;
  if (filter === 'paid') return m.pricing.isFree !== true;
  if (filter === '1m') return (m.contextTokens ?? 0) >= 1_000_000;
  if (filter === 'multimodal') {
    return (m.inputModalities ?? []).some((x) => x !== 'text');
  }
  // The vendor's own retirement marker. Deprecated models stay in the roster by default,
  // but this filter allows readers to see only models they can actively build on.
  if (filter === 'current') return m.lifecycle !== 'deprecated';
  return true;
}

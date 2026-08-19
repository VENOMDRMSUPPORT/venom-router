/**
 * One cell's value, and — when there is no value — WHICH kind of absence it is.
 *
 * Four states have to be tellable apart at a glance, because they are four
 * different facts about the world and a bare em dash collapses them into one:
 *
 *   known           a value, from a source, with provenance
 *   not applicable  the question does not apply here (a subscription has no
 *                   per-token price to publish) — an answer, not a hole
 *   missing         nobody published it. A real gap in our data
 *   conflicted      sources contradicted each other and we refused to pick
 *
 * The failure this exists to prevent is the oldest one in this catalog: a table
 * where every row looks uniform, so a reader concludes the data is complete.
 */

import type { ApiModel } from '../../api/client';
import styles from './FactState.module.css';

export type FactStateKind = 'known' | 'notApplicable' | 'missing' | 'conflicted';

/**
 * Decide a field's state from the model.
 *
 * Ordering matters: a conflict is reported ahead of a plain absence because it
 * is the more specific truth — the value is unknown *because two sellers
 * disagreed*, which is a different thing from nobody having said anything.
 */
export function factStateOf(
  model: ApiModel,
  field: string,
  value: unknown,
  opts: { notApplicable?: boolean } = {},
): FactStateKind {
  if (opts.notApplicable) return 'notApplicable';
  if (value !== null && value !== undefined) return 'known';
  if ((model.conflicts ?? []).some((c) => c.field === field)) return 'conflicted';
  return 'missing';
}

const LABEL: Record<FactStateKind, string> = {
  known: '',
  notApplicable: 'n/a',
  missing: 'missing',
  conflicted: 'conflict',
};

const TITLE: Record<FactStateKind, string> = {
  known: '',
  notApplicable: 'Not applicable to this offering — an answer, not a gap.',
  missing: 'No source published this. A real gap in our data.',
  conflicted: 'Sources disagreed, so no value was taken. Open the evidence to see every side.',
};

export function FactState({ state, children }: { state: FactStateKind; children?: React.ReactNode }) {
  if (state === 'known') return <>{children}</>;
  return (
    <span className={`${styles.chip} ${styles[state]}`} title={TITLE[state]} data-state={state}>
      {LABEL[state]}
    </span>
  );
}

/**
 * Evaluating one model, with the work visible while it happens.
 *
 * The service owns the job; this only shows it and asks it to stop. Three
 * states: what a click will cost, what is happening, and what came out.
 *
 * The cost preview is not decoration. A click here spends paid provider
 * requests, and the run this component replaces spent an hour producing wrong
 * numbers that nobody could see being written.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  fetchEvaluationPlan,
  fetchEvaluationState,
  startEvaluation,
  stopEvaluations,
  type ApiModel,
  type EvaluationPlanView,
  type EvaluationStateView,
} from '../../api/client';
import { useCatalog } from '../../hooks/useCatalog';
import styles from './EvaluateModal.module.css';

const POLL_INTERVAL_MS = 1500;

const DIMENSION_LABELS: Record<string, string> = {
  coding: 'Coding',
  reasoning: 'Reasoning',
  longContext: 'Long context',
  toolCalling: 'Tool calling',
  structuredOutput: 'Structured output',
  vision: 'Vision',
  speed: 'Speed',
};

const BLOCKED_EXPLANATIONS: Record<string, string> = {
  model_not_found: 'This model is not in the catalog.',
  identity_unresolved: 'No proven model identity, so evidence cannot be attributed to anything.',
  // Names the fix, not just the symptom. This one sentence covered two unrelated
  // causes — an env file nothing loaded, and a variable name corrupted by a BOM —
  // and named neither, so it survived every attempt to fix it looking identical.
  missing_credentials: 'This process cannot read an API key for this provider. Set it in catalog/.env and '
    + 'restart the service — `npm run env:check` names the exact variable and says whether the file was '
    + 'loaded at all.',
};

const label = (dimension: string) => DIMENSION_LABELS[dimension] ?? dimension;

export function EvaluateModal({ model, onClose }: { model: ApiModel; onClose: () => void }) {
  const { reload } = useCatalog();
  const [plan, setPlan] = useState<EvaluationPlanView | null>(null);
  const [state, setState] = useState<EvaluationStateView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [finished, setFinished] = useState(false);
  const wasRunning = useRef(false);

  const mine = state?.current && state.current.providerId === model.providerId
    && state.current.modelId === model.modelId
    ? state.current
    : null;
  const queued = (state?.queue ?? []).some(
    (job) => job.providerId === model.providerId && job.modelId === model.modelId,
  );

  const refreshState = useCallback(async () => {
    try {
      setState(await fetchEvaluationState());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [nextPlan, nextState] = await Promise.all([
          fetchEvaluationPlan(model.providerId, model.modelId),
          fetchEvaluationState(),
        ]);
        if (cancelled) return;
        setPlan(nextPlan);
        setState(nextState);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => { cancelled = true; };
  }, [model.providerId, model.modelId]);

  // Polls only while something is actually running, and never after unmount.
  useEffect(() => {
    if (!state || state.state === 'idle') return undefined;
    const timer = setInterval(() => { void refreshState(); }, POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [state, refreshState]);

  // When this model's job leaves the runner, the table behind the modal is stale.
  useEffect(() => {
    if (mine) {
      wasRunning.current = true;
      return;
    }
    if (wasRunning.current) {
      wasRunning.current = false;
      setFinished(true);
      reload();
      void fetchEvaluationPlan(model.providerId, model.modelId).then(setPlan).catch(() => {});
    }
  }, [mine, reload, model.providerId, model.modelId]);

  const onStart = async () => {
    setBusy(true);
    setError(null);
    const outcome = await startEvaluation(model.providerId, model.modelId);
    if (!outcome.ok) setError(BLOCKED_EXPLANATIONS[outcome.reason] ?? outcome.reason);
    await refreshState();
    setBusy(false);
  };

  const onStop = async () => {
    setBusy(true);
    await stopEvaluations();
    await refreshState();
    setBusy(false);
  };

  const rows = mine
    ? [...mine.dimensionsCompleted.map((entry) => ({ ...entry, active: false })),
       ...(mine.dimension && !mine.dimensionsCompleted.some((entry) => entry.dimension === mine.dimension)
         ? [{ dimension: mine.dimension, score: null, status: 'running', active: true }]
         : []),
       ...mine.dimensionsRemaining
         .filter((entry) => entry !== mine.dimension)
         .map((entry) => ({ dimension: entry, score: null, status: 'pending', active: false }))]
    : [];

  return (
    <div className={styles.backdrop} onClick={onClose} role="dialog" aria-modal="true"
      aria-label={`Evaluate ${model.modelId}`}>
      <div className={styles.dialog} onClick={(event) => event.stopPropagation()}>
        <header className={styles.header}>
          <div>
            <h2 className={styles.title}>{model.modelId}</h2>
            <span className={styles.subtitle}>{model.providerId}</span>
          </div>
          <button type="button" className={styles.close} onClick={onClose} aria-label="Close">ESC</button>
        </header>

        {error && <p className={styles.error} role="alert">{error}</p>}

        {!plan && !error && <p className={styles.muted}>Reading what this model still needs…</p>}

        {plan?.blocked && !mine && (
          <p className={styles.blocked} data-testid="evaluate-blocked">
            {BLOCKED_EXPLANATIONS[plan.blocked] ?? plan.blocked}
            <span className={styles.reasonCode}>{plan.blocked}</span>
          </p>
        )}

        {plan && !plan.blocked && !mine && (
          <div className={styles.preview}>
            {plan.dimensions.length === 0 && plan.speed === 'scored' ? (
              <p className={styles.muted} data-testid="evaluate-nothing-missing">
                Every applicable dimension is already scored. Nothing to run.
              </p>
            ) : (
              <>
                <ul className={styles.dimensionList}>
                  {plan.dimensions.map((dimension) => (
                    <li key={dimension} className={styles.dimensionRow}>
                      <span>{label(dimension)}</span>
                      <span className={styles.pending}>missing</span>
                    </li>
                  ))}
                  {plan.speed === 'missing' && (
                    <li className={styles.dimensionRow}>
                      <span>{label('speed')}</span>
                      <span className={styles.pending}>missing</span>
                    </li>
                  )}
                </ul>
                <p className={styles.cost} data-testid="evaluate-cost">
                  About <strong>{plan.estimatedRequests}</strong> paid requests to {model.providerId}.
                </p>
                <button type="button" className={styles.start} onClick={() => void onStart()} disabled={busy}>
                  {queued ? 'Queued' : 'Start evaluation'}
                </button>
              </>
            )}
            {plan.skipped.length > 0 && (
              <p className={styles.muted}>
                Not run: {plan.skipped.map((entry) => `${label(entry.dimension)} (${entry.reason.replace('_', ' ')})`).join(', ')}
              </p>
            )}
            {finished && <p className={styles.done} data-testid="evaluate-finished">Run finished. The table has been refreshed.</p>}
          </div>
        )}

        {mine && (
          <div className={styles.running} data-testid="evaluate-running">
            <ul className={styles.dimensionList}>
              {rows.map((row) => (
                <li key={row.dimension} className={styles.dimensionRow}>
                  <span>{label(row.dimension)}</span>
                  {row.active ? (
                    <span className={styles.progress} data-testid="evaluate-progress">
                      {mine.samplesCompleted} / {mine.samplesTotal}
                    </span>
                  ) : row.status === 'pending' ? (
                    <span className={styles.pending}>waiting</span>
                  ) : row.score !== null ? (
                    <span className={styles.score}>{row.score.toFixed(1)}</span>
                  ) : (
                    <span className={styles.incomplete}>{row.status.replace('_', ' ')}</span>
                  )}
                </li>
              ))}
            </ul>
            <button type="button" className={styles.stop} onClick={() => void onStop()} disabled={busy}>
              Stop
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

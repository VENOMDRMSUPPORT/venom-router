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
  LuSlidersHorizontal,
  LuPlay,
  LuSquare,
  LuRotateCcw,
  LuInfo,
  LuCircleAlert,
  LuCheck,
  LuClock,
  LuCoins,
  LuLayers,
  LuX,
} from 'react-icons/lu';
import {
  fetchEvaluationDetail,
  fetchEvaluationState,
  regradeEvaluation,
  startEvaluation,
  stopEvaluations,
  isUnreachable,
  SERVICE_UNREACHABLE,
  type ApiModel,
  type EvaluationDetailView,
  type EvaluationEvidenceView,
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
  costEfficiency: 'Cost efficiency',
};

/** Reading order for the evidence panel: quality first, then operational. */
const DIMENSION_ORDER = [
  'coding', 'reasoning', 'longContext', 'toolCalling', 'structuredOutput', 'vision', 'speed', 'costEfficiency',
];

/**
 * A stopped service, in the one sentence that ends the search.
 *
 * Shared by both refusal maps because it is the same fact either way, and it is
 * the only entry here that is not about this model at all — every other reason
 * describes something the catalog knows and this one describes the catalog not
 * being reachable to ask.
 */
const SERVICE_DOWN = 'The catalog service is not answering. Nothing is wrong with this model — the API '
  + 'process is not running, so no request reached it. Start it and reopen this panel.';

const BLOCKED_EXPLANATIONS: Record<string, string> = {
  [SERVICE_UNREACHABLE]: SERVICE_DOWN,
  model_not_found: 'This model is not in the catalog.',
  identity_unresolved: 'No proven model identity, so evidence cannot be attributed to anything.',
  // Names the fix, not just the symptom. This one sentence covered two unrelated
  // causes — an env file nothing loaded, and a variable name corrupted by a BOM —
  // and named neither, so it survived every attempt to fix it looking identical.
  missing_credentials: 'This process cannot read an API key for this provider. Set it in catalog/.env and '
    + 'restart the service — `npm run env:check` names the exact variable and says whether the file was '
    + 'loaded at all.',
};

/**
 * Why a re-read was refused, in words that name the fix.
 *
 * The route answers with a short machine reason, and showing that raw put "an
 * evaluation is running" on screen — lower case, no punctuation, indistinguishable
 * from nothing having happened. The same lesson as `missing_credentials` above: a
 * message that names the symptom and not the remedy gets read as a dead button.
 */
const REREAD_REFUSALS: Record<string, string> = {
  [SERVICE_UNREACHABLE]: SERVICE_DOWN,
  'an evaluation is running': 'A paid evaluation is running right now. Re-reading a dimension while it is '
    + 'being measured would publish a number from half a run, so this waits until the queue is empty.',
  'no resolved identity to re-read': 'This offer has no proven model identity, so there is no body of '
    + 'evidence to re-read.',
};

/**
 * What to put on screen when a read failed.
 *
 * A stopped service reaches this panel through four different routes — two that
 * throw and two that return a reason — and without this it said the same thing
 * in two different wordings depending on which one the owner tripped first.
 */
const explainFailure = (cause: unknown): string => {
  if (isUnreachable(cause)) return SERVICE_DOWN;
  return cause instanceof Error ? cause.message : String(cause);
};

const label = (dimension: string) => DIMENSION_LABELS[dimension] ?? dimension;

/**
 * How a stored number came to exist, in the words the evidence itself uses.
 *
 * The trail is not decoration here. Three of these read identically as a score
 * and mean completely different things about whether it can be trusted, and a
 * withdrawn dimension has to say why it is empty or it looks like a gap nobody
 * has got round to.
 */
function describeEvidence(row: EvaluationEvidenceView): string {
  const trail = row.evidence.join(' ');
  const when = row.evaluatedAt?.slice(0, 10) ?? 'unknown date';
  if (trail.includes('withdrawn:answer-truncated')) {
    return `withdrawn ${when} — the provider never finished an answer`;
  }
  if (trail.includes('regraded:retained-responses')) return `re-read from stored responses, ${when}`;
  if (trail.includes('catalog-operational-facts')) return `from catalog facts, ${when}`;
  return `measured against the provider, ${when}`;
}

export function EvaluateModal({ model, onClose }: { model: ApiModel; onClose: () => void }) {
  const { reload } = useCatalog();
  const [detail, setDetail] = useState<EvaluationDetailView | null>(null);
  const [state, setState] = useState<EvaluationStateView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [finished, setFinished] = useState(false);
  const [reread, setReread] = useState<string | null>(null);
  const wasRunning = useRef(false);
  const plan = detail?.plan ?? null;

  const mine = state?.current && state.current.providerId === model.providerId
    && state.current.modelId === model.modelId
    ? state.current
    : null;
  const queued = (state?.queue ?? []).some(
    (job) => job.providerId === model.providerId && job.modelId === model.modelId,
  );
  /** Any job at all, not just this offer's: the re-read route refuses on either. */
  const runnerBusy = state !== null && state.state !== 'idle';

  const refreshState = useCallback(async () => {
    try {
      setState(await fetchEvaluationState());
    } catch (cause) {
      setError(explainFailure(cause));
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [nextDetail, nextState] = await Promise.all([
          fetchEvaluationDetail(model.providerId, model.modelId),
          fetchEvaluationState(),
        ]);
        if (cancelled) return;
        setDetail(nextDetail);
        setState(nextState);
      } catch (cause) {
        if (!cancelled) setError(explainFailure(cause));
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
      void fetchEvaluationDetail(model.providerId, model.modelId).then(setDetail).catch(() => {});
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

  /**
   * Re-read what is already stored. Zero paid requests, so no cost preview.
   *
   * The outcome is reported in numbers rather than a bare "done", because the
   * three things this can do — re-derive a number, change it, or withdraw it —
   * are not interchangeable, and a withdrawal removes a published score.
   */
  const onReread = async () => {
    setBusy(true);
    setError(null);
    setReread(null);
    const result = await regradeEvaluation(model.providerId, model.modelId);
    if (!result.ok) {
      setError(REREAD_REFUSALS[result.reason] ?? result.reason);
    } else {
      const changed = result.outcome.rescored.filter(
        (row) => row.before === null || Math.abs(row.after - row.before) >= 0.05,
      ).length;
      // "0 changed" is the commonest outcome and the easiest to mistake for a
      // broken button, so it says so in words rather than in three zeroes.
      setReread(changed === 0 && result.outcome.withdrawn === 0
        ? `Re-read ${result.outcome.rescored.length} dimension(s). Nothing changed — every stored response `
          + 'already reads the same under the current grader.'
        : `Re-read ${result.outcome.rescored.length} dimension(s): ${changed} changed`
          + `${result.outcome.withdrawn > 0 ? `, ${result.outcome.withdrawn} withdrawn` : ''}.`);
      reload();
      try {
        setDetail(await fetchEvaluationDetail(model.providerId, model.modelId));
      } catch { /* the table already reloaded; the panel refreshes on reopen */ }
    }
    setBusy(false);
  };

  const onStop = async () => {
    setBusy(true);
    await stopEvaluations();
    await refreshState();
    setBusy(false);
  };

  /**
   * Every dimension that carries a number, or says why it no longer does.
   *
   * Quality lives on the identity and the two operational dimensions on the
   * offer, so both are read — and a row with neither a score nor a withdrawal is
   * left out, because "this model supports vision" is not evidence about it.
   */
  const evidence = detail
    ? [
        ...detail.identityDimensions,
        ...detail.offerDimensions.filter((row) => row.dimension === 'speed' || row.dimension === 'costEfficiency'),
      ]
      .filter((row) => row.score !== null || row.evidence.some((entry) => entry.startsWith('withdrawn:')))
      .sort((left, right) => DIMENSION_ORDER.indexOf(left.dimension) - DIMENSION_ORDER.indexOf(right.dimension))
    : [];

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
          <div className={styles.headerLeft}>
            <div className={styles.iconBox}>
              <LuSlidersHorizontal size={16} />
            </div>
            <div className={styles.headerMeta}>
              <div className={styles.titleRow}>
                <h2 className={styles.title}>{model.modelId}</h2>
                <span className={styles.providerBadge}>{model.providerId}</span>
              </div>
              <span className={styles.subtitle}>Model Evaluation & Quality Evidence</span>
            </div>
          </div>
          <button type="button" className={styles.close} onClick={onClose} aria-label="Close">
            <kbd className={styles.escKbd}>ESC</kbd>
            <LuX size={14} />
          </button>
        </header>

        <div className={styles.body}>
          {error && (
            <div className={styles.error} role="alert">
              <LuCircleAlert size={16} className={styles.errorIcon} />
              <span>{error}</span>
            </div>
          )}

          {!plan && !error && (
            <div className={styles.loadingState}>
              <div className={styles.loadingSpinner} />
              <p className={styles.muted}>Reading what this model still needs…</p>
            </div>
          )}

          {plan?.blocked && !mine && (
            <div className={styles.blockedCard} data-testid="evaluate-blocked">
              <div className={styles.blockedHeader}>
                <LuCircleAlert size={16} className={styles.blockedIcon} />
                <span className={styles.blockedTitle}>Evaluation Blocked</span>
                <span className={styles.reasonCode}>{plan.blocked}</span>
              </div>
              <p className={styles.blockedMessage}>
                {BLOCKED_EXPLANATIONS[plan.blocked] ?? plan.blocked}
              </p>
            </div>
          )}

          {plan && !plan.blocked && !mine && (
            <div className={styles.preview}>
              {(plan.dimensions ?? []).length === 0 && plan.speed === 'scored' ? (
                <div className={styles.allScoredCard} data-testid="evaluate-nothing-missing">
                  <div className={styles.allScoredIcon}>
                    <LuCheck size={16} />
                  </div>
                  <p className={styles.allScoredText}>
                    Every applicable dimension is already scored.
                  </p>
                </div>
              ) : (
                <div className={styles.sectionCard}>
                  <div className={styles.sectionHeader}>
                    <div className={styles.sectionTitleRow}>
                      <LuPlay size={14} className={styles.sectionIcon} />
                      <h3 className={styles.sectionTitle}>Required Measurements</h3>
                    </div>
                    <span className={styles.badgePendingCount}>
                      {(plan.dimensions ?? []).length + (plan.speed === 'missing' ? 1 : 0)} Missing
                    </span>
                  </div>

                  <ul className={styles.dimensionList}>
                    {(plan.dimensions ?? []).map((dimension) => (
                      <li key={dimension} className={styles.dimensionRow}>
                        <span className={styles.dimensionName}>{label(dimension)}</span>
                        <span className={styles.pendingBadge}>missing</span>
                      </li>
                    ))}
                    {plan.speed === 'missing' && (
                      <li className={styles.dimensionRow}>
                        <span className={styles.dimensionName}>{label('speed')}</span>
                        <span className={styles.pendingBadge}>missing</span>
                      </li>
                    )}
                  </ul>

                  <div className={styles.actionRow}>
                    <div className={styles.costInfo}>
                      <LuCoins size={15} className={styles.costIcon} />
                      <p className={styles.cost} data-testid="evaluate-cost">
                        About <strong>{plan.estimatedRequests ?? 0}</strong> paid requests to {model.providerId}.
                      </p>
                    </div>
                    <button
                      type="button"
                      className={styles.primaryButton}
                      onClick={() => void onStart()}
                      disabled={busy}
                    >
                      <LuPlay size={13} />
                      <span>{queued ? 'Queued' : 'Start evaluation'}</span>
                    </button>
                  </div>
                </div>
              )}

              {(plan.skipped ?? []).length > 0 && (
                <div className={styles.skippedContainer}>
                  <span className={styles.skippedLabel}>Not run:</span>
                  <div className={styles.tagGroup}>
                    {(plan.skipped ?? []).map((entry) => (
                      <span key={entry.dimension} className={styles.skippedTag}>
                        {`${label(entry.dimension)} (${entry.reason.replace('_', ' ')})`}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {/*
                Shown whatever the plan says, because "already scored" is an answer
                and this dialog used to treat it as a dead end. The endpoint was
                already returning every one of these facts; the client kept `.plan`
                and dropped them.
              */}
              {evidence.length > 0 && (
                <div className={styles.sectionCard} data-testid="evaluate-evidence">
                  <div className={styles.sectionHeader}>
                    <div className={styles.sectionTitleRow}>
                      <LuLayers size={14} className={styles.sectionIcon} />
                      <h3 className={styles.sectionTitle}>What the current scores rest on:</h3>
                    </div>
                    <span className={styles.evidenceCountBadge}>
                      {evidence.length} Dimensions
                    </span>
                  </div>

                  <div className={styles.tableWrapper}>
                    <table className={styles.evidenceTable}>
                      <thead>
                        <tr>
                          <th className={styles.thDimension}>Dimension</th>
                          <th className={styles.thScore}>Score</th>
                          <th className={styles.thSource}>Evidence & Source</th>
                        </tr>
                      </thead>
                      <tbody>
                        {evidence.map((row) => (
                          <tr key={row.dimension} className={styles.tableRow}>
                            <td className={styles.tdDimension}>
                              <span className={styles.dimensionLabel}>{label(row.dimension)}</span>
                            </td>
                            <td className={styles.tdScore}>
                              <span className={row.score === null ? styles.scoreUnknown : styles.scorePill}>
                                {row.score === null ? 'unknown' : row.score.toFixed(1)}
                              </span>
                            </td>
                            <td className={styles.tdSource}>
                              <span className={styles.evidenceDescription}>
                                {describeEvidence(row)}
                              </span>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>

                  <div className={styles.rereadActionWrapper}>
                    <div className={styles.rereadInfo}>
                      <LuInfo size={15} className={styles.rereadIcon} />
                      <p className={styles.cost}>
                        {runnerBusy
                          // Refusing the click before it is made. The modal already polls
                          // the runner, so letting it be clicked into a 409 was a dead
                          // button that looked like a bug rather than a rule.
                          ? 'Available once the running evaluation finishes — re-reading a dimension mid-measurement '
                            + 'would publish a number from half a run.'
                          : 'No paid requests: it re-reads responses this catalog already bought.'}
                      </p>
                    </div>
                    <button
                      type="button"
                      className={styles.secondaryButton}
                      onClick={() => void onReread()}
                      disabled={busy || runnerBusy}
                      title={runnerBusy ? 'Waits for the running evaluation to finish' : undefined}
                    >
                      <LuRotateCcw size={13} />
                      <span>Re-read stored evidence</span>
                    </button>
                  </div>
                </div>
              )}

              {reread && (
                <div className={styles.statusSuccess} data-testid="evaluate-reread">
                  <LuCheck size={15} className={styles.statusIcon} />
                  <span>{reread}</span>
                </div>
              )}
              {finished && (
                <div className={styles.statusSuccess} data-testid="evaluate-finished">
                  <LuCheck size={15} className={styles.statusIcon} />
                  <span>Run finished. The table has been refreshed.</span>
                </div>
              )}
            </div>
          )}

          {mine && (
            <div className={styles.runningCard} data-testid="evaluate-running">
              <div className={styles.sectionHeader}>
                <div className={styles.sectionTitleRow}>
                  <span className={styles.pulsingDot} />
                  <h3 className={styles.sectionTitle}>Live Evaluation in Progress</h3>
                </div>
                {mine.dimension && (
                  <span className={styles.activeDimBadge}>Active: {label(mine.dimension)}</span>
                )}
              </div>

              <ul className={styles.dimensionList}>
                {rows.map((row) => (
                  <li key={row.dimension} className={`${styles.dimensionRow} ${row.active ? styles.rowActive : ''}`}>
                    <div className={styles.rowLeft}>
                      {row.active ? (
                        <span className={styles.liveSpinner} />
                      ) : row.score !== null ? (
                        <LuCheck size={14} className={styles.rowDoneIcon} />
                      ) : (
                        <LuClock size={14} className={styles.rowWaitIcon} />
                      )}
                      <span className={styles.dimensionName}>{label(row.dimension)}</span>
                    </div>

                    <div className={styles.rowRight}>
                      {row.active ? (
                        <span className={styles.progress} data-testid="evaluate-progress">
                          {mine.samplesCompleted} / {mine.samplesTotal}
                        </span>
                      ) : row.status === 'pending' ? (
                        <span className={styles.pendingBadge}>waiting</span>
                      ) : row.score !== null ? (
                        <span className={styles.scorePill}>{row.score.toFixed(1)}</span>
                      ) : (
                        <span className={styles.incompleteBadge}>{row.status.replace('_', ' ')}</span>
                      )}
                    </div>
                  </li>
                ))}
              </ul>

              <div className={styles.stopActionRow}>
                <span className={styles.runningHint}>
                  Evaluation runs in background. You can safely stop it.
                </span>
                <button type="button" className={styles.stopButton} onClick={() => void onStop()} disabled={busy}>
                  <LuSquare size={13} />
                  <span>Stop</span>
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

import { describe, expect, test, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import type { ApiModel } from '../../api/client';
import { EvaluateModal } from './EvaluateModal';

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  fetchEvaluationDetail: vi.fn(),
  fetchEvaluationState: vi.fn(),
  startEvaluation: vi.fn(),
  stopEvaluations: vi.fn(),
  regradeEvaluation: vi.fn(),
}));

const reload = vi.fn();
vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({ data: null, error: null, loading: false, reload }),
}));

import {
  fetchEvaluationDetail,
  fetchEvaluationState,
  regradeEvaluation,
  startEvaluation,
  stopEvaluations,
} from '../../api/client';
import type { EvaluationEvidenceView, EvaluationPlanView } from '../../api/client';

const model = { providerId: 'p', modelId: 'cline-pass/glm-5.3' } as unknown as ApiModel;

const idle = { state: 'idle' as const, current: null, queue: [] };

/**
 * The endpoint's whole answer, which is what the modal now reads.
 *
 * It used to keep `.plan` and drop the evidence arriving in the same response,
 * which is why a fully-scored model produced one sentence and no way forward.
 */
const detail = (plan: EvaluationPlanView, identityDimensions: EvaluationEvidenceView[] = []) => ({
  identityId: 'z-ai/glm-5.3',
  plan,
  identityDimensions,
  offerDimensions: [],
});

const scoredRow = (over: Partial<EvaluationEvidenceView> = {}): EvaluationEvidenceView => ({
  dimension: 'coding',
  score: 91.4,
  status: 'scored',
  confidence: 0.99,
  sampleCount: 300,
  evidence: ['runtime:p/cline-pass/glm-5.3', 'run:1'],
  evaluatedAt: '2026-08-20T20:46:01.193Z',
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(stopEvaluations).mockResolvedValue(undefined);
  vi.mocked(fetchEvaluationState).mockResolvedValue(idle);
});

describe('EvaluateModal', () => {
  test('shows what the click will cost, and spends nothing until it is clicked', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: ['coding', 'vision'], skipped: [], speed: 'missing', blocked: null, estimatedRequests: 149,
    }));
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByTestId('evaluate-cost')).toHaveTextContent('149');
    expect(screen.getByText('Coding')).toBeInTheDocument();
    expect(screen.getByText('Speed')).toBeInTheDocument();
    expect(startEvaluation).not.toHaveBeenCalled();
  });

  test('names what is already scored rather than hiding it', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: ['coding'],
      skipped: [{ dimension: 'vision', reason: 'unsupported' }],
      speed: 'scored', blocked: null, estimatedRequests: 63,
    }));
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByText(/Vision \(unsupported\)/)).toBeInTheDocument();
  });

  test('offers no start button when the plan is blocked, and explains why', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: [], skipped: [], speed: 'missing', blocked: 'missing_credentials', estimatedRequests: 0,
    }));
    render(<EvaluateModal model={model} onClose={() => {}} />);
    const blocked = await screen.findByTestId('evaluate-blocked');
    // Asserted on what makes the message actionable, not its exact wording: the
    // file to edit and the command that names the variable. The old sentence
    // ("No API key is configured for this provider") named neither, and covered
    // two unrelated causes — an env file nothing loaded, and a variable name
    // corrupted by a BOM — so it could not tell a reader which one they had.
    expect(blocked).toHaveTextContent('cannot read an API key');
    expect(blocked).toHaveTextContent('catalog/.env');
    expect(blocked).toHaveTextContent('npm run env:check');
    expect(blocked).toHaveTextContent('missing_credentials');
    expect(screen.queryByRole('button', { name: /start/i })).not.toBeInTheDocument();
  });

  test('says so when there is genuinely nothing left to run', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: [], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 0,
    }));
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByTestId('evaluate-nothing-missing')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /start evaluation/i })).not.toBeInTheDocument();
  });

  test('a fully scored model is not a dead end: it shows what the scores rest on', async () => {
    // The state the owner actually hit. Nothing to buy, and the dialog offered
    // one sentence — while the same response already carried every score, the
    // date it was taken, and whether it was measured or re-derived.
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail(
      { dimensions: [], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 0 },
      [
        scoredRow({ dimension: 'coding', score: 91.4 }),
        scoredRow({
          dimension: 'reasoning', score: null,
          evidence: ['withdrawn:answer-truncated', 'run:163'],
        }),
      ],
    ));
    render(<EvaluateModal model={model} onClose={() => {}} />);

    const panel = await screen.findByTestId('evaluate-evidence');
    expect(panel).toHaveTextContent('91.4');
    expect(panel).toHaveTextContent('measured against the provider, 2026-08-20');
    // A withdrawn dimension has to say why it is empty, or it reads as a gap.
    expect(panel).toHaveTextContent('unknown');
    expect(panel).toHaveTextContent('never finished an answer');
    expect(screen.getByRole('button', { name: /re-read stored evidence/i })).toBeInTheDocument();
    expect(panel).toHaveTextContent('No paid requests');
  });

  test('a re-read spends nothing and reports what it changed', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail(
      { dimensions: [], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 0 },
      [scoredRow()],
    ));
    vi.mocked(regradeEvaluation).mockResolvedValue({
      ok: true,
      outcome: { rescored: [{ dimension: 'coding', before: 20, after: 99.7 }], withdrawn: 2, unreplayable: 3 },
    });
    render(<EvaluateModal model={model} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('button', { name: /re-read stored evidence/i }));

    expect(await screen.findByTestId('evaluate-reread')).toHaveTextContent('1 changed; 2 withdrawn');
    expect(startEvaluation).not.toHaveBeenCalled();
    await waitFor(() => expect(reload).toHaveBeenCalled());
  });

  test('a refused re-read is shown rather than reported as done', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail(
      { dimensions: [], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 0 },
      [scoredRow()],
    ));
    vi.mocked(regradeEvaluation).mockResolvedValue({ ok: false, reason: 'an evaluation is running' });
    render(<EvaluateModal model={model} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('button', { name: /re-read stored evidence/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent('an evaluation is running');
    expect(screen.queryByTestId('evaluate-reread')).not.toBeInTheDocument();
  });

  test('shows live sample progress for the dimension in flight', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: ['coding'], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 63,
    }));
    vi.mocked(fetchEvaluationState).mockResolvedValue({
      state: 'running',
      current: {
        providerId: 'p', modelId: 'cline-pass/glm-5.3', dimension: 'coding',
        samplesCompleted: 24, samplesTotal: 63,
        dimensionsCompleted: [{ dimension: 'reasoning', score: 91.4, status: 'complete' }],
        dimensionsRemaining: ['coding', 'vision'],
      },
      queue: [],
    });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByTestId('evaluate-progress')).toHaveTextContent('24 / 63');
    // A score that has already landed is shown while the rest is still running.
    expect(screen.getByText('91.4')).toBeInTheDocument();
    expect(screen.getByText('waiting')).toBeInTheDocument();
  });

  test('another model\'s job does not take over this modal', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: ['coding'], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 63,
    }));
    vi.mocked(fetchEvaluationState).mockResolvedValue({
      state: 'running',
      current: {
        providerId: 'p', modelId: 'a-different-model', dimension: 'coding',
        samplesCompleted: 5, samplesTotal: 63,
        dimensionsCompleted: [], dimensionsRemaining: ['coding'],
      },
      queue: [],
    });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByTestId('evaluate-cost')).toBeInTheDocument();
    expect(screen.queryByTestId('evaluate-running')).not.toBeInTheDocument();
  });

  test('stop asks the service to stop', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: ['coding'], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 63,
    }));
    vi.mocked(fetchEvaluationState).mockResolvedValue({
      state: 'running',
      current: {
        providerId: 'p', modelId: 'cline-pass/glm-5.3', dimension: 'coding',
        samplesCompleted: 1, samplesTotal: 63,
        dimensionsCompleted: [], dimensionsRemaining: ['coding'],
      },
      queue: [],
    });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    fireEvent.click(await screen.findByRole('button', { name: /stop/i }));
    await waitFor(() => expect(stopEvaluations).toHaveBeenCalled());
  });

  test('a refusal from the service is shown, not swallowed', async () => {
    vi.mocked(fetchEvaluationDetail).mockResolvedValue(detail({
      dimensions: ['coding'], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 63,
    }));
    vi.mocked(startEvaluation).mockResolvedValue({ ok: false, status: 422, reason: 'identity_unresolved' });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    fireEvent.click(await screen.findByRole('button', { name: /start/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent('No proven model identity');
  });
});

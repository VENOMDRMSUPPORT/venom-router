import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { importInspectEvaluation, type InspectEvaluationLog } from './inspect-import.ts';

function log(overrides: Partial<InspectEvaluationLog> = {}): InspectEvaluationLog {
  return {
    status: 'success',
    eval: {
      task: 'inspect_evals/gpqa_diamond',
      model: 'openai-api/opencode-go/glm-5.3',
      config: { limit: 20, epochs: 3 },
      packages: { inspect_ai: '0.3.259', inspect_evals: '0.17.0' },
    },
    stats: {
      started_at: '2026-08-19T00:00:00.000Z',
      completed_at: '2026-08-19T00:10:00.000Z',
    },
    samples: Array.from({ length: 20 }, (_, index) => Array.from({ length: 3 }, (_, epoch) => ({
      id: `sample-${index + 1}`,
      epoch: epoch + 1,
      scores: { choice: { value: index === 0 && epoch === 0 ? 'I' : 'C' } },
    }))).flat(),
    ...overrides,
  };
}

describe('Inspect evaluation import', () => {
  test('persists 60 individual samples and one smoothed identity dimension', () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('opencode-go','OpenCode Go','https://example.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('opencode-go','glm-5.3','active','2026-08-19','2026-08-19')`).run();
    const result = importInspectEvaluation(db, log(), {
      providerId: 'opencode-go', modelId: 'glm-5.3', identityId: 'z-ai/glm-5.3', dimension: 'reasoning',
    });
    const row = createEvaluationRepository(db).identityDimensions('z-ai/glm-5.3')[0];
    assert.equal(result.status, 'imported');
    assert.equal(result.samples, 60);
    assert.equal(row.sampleCount, 60);
    assert.ok((row.score ?? 100) < 100);
    const count = db.prepare('SELECT COUNT(*) n FROM evaluation_samples').get() as unknown as { n: number };
    assert.equal(count.n, 60);
  });

  test('rejects a partial smoke log without publishing a score', () => {
    const db = openDb(':memory:');
    const partial = log();
    partial.eval.config.limit = 1;
    partial.samples = partial.samples.slice(0, 3);
    assert.throws(() => importInspectEvaluation(db, partial, {
      providerId: 'opencode-go', modelId: 'glm-5.3', identityId: 'z-ai/glm-5.3', dimension: 'reasoning',
    }), /requires exactly 20 samples x 3 epochs/);
    assert.equal(createEvaluationRepository(db).identityDimensions('z-ai/glm-5.3').length, 0);
  });

  test('rejects a task that does not match the dimension crosswalk', () => {
    const db = openDb(':memory:');
    const wrong = log();
    wrong.eval.task = 'inspect_evals/humaneval';
    assert.throws(() => importInspectEvaluation(db, wrong, {
      providerId: 'opencode-go', modelId: 'glm-5.3', identityId: 'z-ai/glm-5.3', dimension: 'reasoning',
    }), /task does not match/);
  });
});

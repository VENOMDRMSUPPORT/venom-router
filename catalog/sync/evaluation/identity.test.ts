import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { parseEvaluationIdentities, readEvaluationIdentity } from './identity.ts';

describe('evaluation identity overlay', () => {
  test('accepts an exact provider-scoped declaration', () => {
    const parsed = parseEvaluationIdentities({
      version: 1,
      entries: {
        'opencode-zen/big-pickle': {
          kind: 'provider_scoped',
          identityId: 'opencode-zen/big-pickle',
          consent: 'not_required',
          sourceUrl: 'https://opencode.ai/docs/zen',
          evidence: ['OpenCode documents Big Pickle as a stealth model.'],
          reviewedAt: '2026-08-20',
        },
      },
    });
    assert.equal(parsed['opencode-zen/big-pickle']?.kind, 'provider_scoped');
  });

  test('rejects an entry without a reviewed source', () => {
    assert.throws(() => parseEvaluationIdentities({
      version: 1,
      entries: {
        'opencode-zen/muse-spark-1.2-contributor-free': {
          kind: 'benchmark',
          identityId: 'meta/muse-spark-1.2',
          consent: 'granted',
          sourceUrl: 'not-a-url',
          evidence: ['missing source'],
          reviewedAt: '2026-08-20',
        },
      },
    }), /sourceUrl/);
  });

  test('reads the persisted fact without guessing a sibling identity', () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('opencode-zen','Zen','https://opencode.ai/zen')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('opencode-zen','big-pickle','active','2026-08-20','2026-08-20')`).run();
    db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
      VALUES ('opencode-zen','big-pickle','evaluationIdentity',?,'reviewed_source','2026-08-20')`)
      .run(JSON.stringify({ id: 'opencode-zen/big-pickle', kind: 'provider_scoped', consent: 'not_required' }));
    assert.deepEqual(readEvaluationIdentity(db, 'opencode-zen', 'big-pickle'), {
      id: 'opencode-zen/big-pickle', kind: 'provider_scoped', consent: 'not_required',
    });
    db.close();
  });
});

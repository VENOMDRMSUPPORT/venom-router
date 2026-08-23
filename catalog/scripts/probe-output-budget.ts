#!/usr/bin/env node
/**
 * Prove the output budget is enough BEFORE paying for a corpus.
 *
 * A quality fixture asks for `OVERALL_SCORE_POLICY.outputTokens` and the
 * transport retries once at `truncationRetryOutputTokens` when the answer comes
 * back cut off. Whether that ceiling is high enough for a given reasoning model
 * is a fact about that model, and the retained corpus cannot answer it: every
 * truncated sample is censored at the old cap, so the tokens it WOULD have used
 * are unknown. Guessing costs a full re-run to find out.
 *
 * This sends the real fixture, unmodified, for one scenario per offer, and
 * reports what came back. Two requests per offer at worst, against a run that
 * costs sixty-three per dimension.
 *
 *   node --env-file-if-exists=.env scripts/probe-output-budget.ts \
 *     --offers="opencode-go,hy3,coding;ollama-cloud,gpt-oss:20b,reasoning"
 *
 * Entries are separated by `;` and fields by `,` — model ids here contain `/`
 * and `:`, so neither can be the separator. `--scenario=N` picks a different
 * scenario (default 1); `--max-tokens=N` overrides the fixture budget, which is
 * how a higher ceiling is tested before it is committed to policy.
 *
 * Reads no database, so it runs while the service holds it. Never prints a
 * credential: only the outcome, the finish reason, and the token count.
 */
import { createEvaluationTransport, resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';
import { buildEvaluationFixtures, ranOutOfRoom } from '../sync/evaluation/fixtures.ts';
import { OVERALL_SCORE_POLICY, QUALITY_DIMENSIONS, type QualityDimension } from '../sync/evaluation/score.ts';

const valueOf = (name: string): string | null => {
  const prefix = `--${name}=`;
  return process.argv.find((arg) => arg.startsWith(prefix))?.slice(prefix.length) ?? null;
};

const raw = valueOf('offers');
if (!raw) throw new Error('usage: --offers="providerId,modelId,dimension;..."');
const scenarioIndex = Number(valueOf('scenario') ?? 1) - 1;
const overrideBudget = valueOf('max-tokens') === null ? null : Number(valueOf('max-tokens'));

const offers = raw.split(';').filter(Boolean).map((entry) => {
  const [providerId, modelId, dimension, scenarioId] = entry.split(',').map((part) => part.trim());
  if (!providerId || !modelId || !dimension) throw new Error(`unparsable_offer:${entry}`);
  if (!QUALITY_DIMENSIONS.includes(dimension as QualityDimension)) throw new Error(`unknown_dimension:${dimension}`);
  return { providerId, modelId, dimension: dimension as QualityDimension, scenarioId: scenarioId || null };
});

const fixtures = buildEvaluationFixtures();

interface Reading {
  offer: string;
  dimension: string;
  outcome: string;
  /** The provider's own word for why it stopped, which is what decides truncation. */
  finishReason: string | null;
  outputTokens: number | null;
  attempts: number;
  /** Out of five. A cut-off answer has none, which is the point. */
  criteriaPassed: number | null;
  verdict: string;
}

const readings: Reading[] = [];

for (const offer of offers) {
  const label = `${offer.providerId}/${offer.modelId}`;
  const credential = resolveEvaluationCredential(offer.providerId);
  if (!credential) {
    readings.push({
      offer: label, dimension: offer.dimension, outcome: 'missing_credentials',
      finishReason: null, outputTokens: null, attempts: 0, criteriaPassed: null,
      verdict: 'cannot probe — no key for this provider in this process',
    });
    continue;
  }

  const scenario = offer.scenarioId
    ? fixtures[offer.dimension].find((entry) => entry.id === offer.scenarioId)
    : fixtures[offer.dimension][scenarioIndex];
  if (!scenario) throw new Error(`no_such_scenario:${offer.dimension}#${offer.scenarioId ?? scenarioIndex + 1}`);
  const payload = overrideBudget === null
    ? scenario.payload
    : { ...scenario.payload as Record<string, unknown>, max_tokens: overrideBudget };

  const transport = createEvaluationTransport({ providerId: offer.providerId, modelId: offer.modelId, credential });
  const outcome = await transport(payload, credential);

  if (outcome.kind !== 'success') {
    readings.push({
      offer: label, dimension: offer.dimension, outcome: outcome.kind,
      finishReason: null, outputTokens: null, attempts: outcome.attempts, criteriaPassed: null,
      verdict: `provider refused: ${outcome.errorCode}`,
    });
    continue;
  }

  const body = outcome.response.body as {
    choices?: Array<{ finish_reason?: unknown }>;
    usage?: { completion_tokens?: unknown; output_tokens?: unknown };
  };
  const finishReason = typeof body.choices?.[0]?.finish_reason === 'string' ? body.choices[0].finish_reason : null;
  const usage = body.usage ?? {};
  const outputTokens = typeof usage.completion_tokens === 'number'
    ? usage.completion_tokens
    : typeof usage.output_tokens === 'number' ? usage.output_tokens : null;
  const graded = scenario.grade(outcome.response.body);

  // The same rule `runtime.ts` applies, so the probe agrees with the run it is
  // predicting: a cut-off response is evidence only if it scored full marks.
  const fullMarks = graded.weightedSuccesses === graded.weightedCriteria;
  const cutOff = ranOutOfRoom(outcome.response.body);
  readings.push({
    offer: label, dimension: offer.dimension, outcome: 'success',
    finishReason, outputTokens, attempts: outcome.attempts,
    criteriaPassed: graded.weightedSuccesses,
    verdict: fullMarks
      ? 'answered and graded clean'
      : cutOff
        // The reason this script exists: the ceiling is the thing to raise, and
        // finding out here costs two requests instead of sixty-three.
        ? `STILL CUT OFF at ${overrideBudget ?? OVERALL_SCORE_POLICY.truncationRetryOutputTokens} — this dimension cannot score until the ceiling clears it`
        : `answered in full but scored ${graded.weightedSuccesses}/${graded.weightedCriteria} — a real result, not a budget problem`,
  });
}

for (const row of readings) {
  console.log(
    `${row.offer.padEnd(34)} ${row.dimension.padEnd(15)} ${row.outcome.padEnd(17)} `
    + `finish=${String(row.finishReason ?? '—').padEnd(11)} out=${String(row.outputTokens ?? '—').padStart(6)} `
    + `tries=${row.attempts} ${String(row.criteriaPassed ?? '—').padStart(2)}/5  ${row.verdict}`,
  );
}

const blocked = readings.filter((row) => row.verdict.startsWith('STILL CUT OFF'));
console.log(`\n${readings.length} probed · ${readings.filter((r) => r.criteriaPassed === 5).length} answered clean · ${blocked.length} still cut off`);
if (blocked.length > 0) {
  console.log('DO NOT run the corpus yet. Raise OVERALL_SCORE_POLICY.truncationRetryOutputTokens and probe again:');
  for (const row of blocked) console.log(`  ${row.offer} ${row.dimension}`);
  process.exit(1);
}

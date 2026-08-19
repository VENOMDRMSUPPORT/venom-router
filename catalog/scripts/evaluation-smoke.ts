#!/usr/bin/env node
import { createEvaluationTransport, resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';

const OFFERS = [
  ['ollama-cloud', 'kimi-k3'],
  ['opencode-go', 'glm-5.3'],
  ['opencode-zen', 'mimo-v2.5-free'],
  ['clinepass', 'cline-pass/glm-5.2'],
] as const;

const results = [];
for (const [providerId, modelId] of OFFERS) {
  const credential = resolveEvaluationCredential(providerId);
  if (!credential) {
    results.push({ providerId, modelId, kind: 'missing_credentials' });
    continue;
  }
  const transport = createEvaluationTransport({ providerId, modelId, credential });
  const outcome = await transport({
    messages: [{ role: 'user', content: 'Reply with exactly OK.' }],
    temperature: 0,
    max_tokens: 8,
  }, credential);
  results.push({
    providerId,
    modelId,
    kind: outcome.kind,
    status: 'status' in outcome ? outcome.status : null,
    errorCode: 'errorCode' in outcome ? outcome.errorCode : null,
    attempts: outcome.attempts,
  });
}

console.log(JSON.stringify(results, null, 2));

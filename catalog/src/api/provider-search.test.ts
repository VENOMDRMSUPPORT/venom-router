import { describe, expect, test } from 'vitest';
import type { ApiModel, ApiProvider } from './client';
import { matchesProviderSearch } from './provider-search';

function provider(over: Partial<ApiProvider> = {}): ApiProvider {
  return {
    id: 'openrouter',
    name: 'OpenRouter',
    rosterUrl: 'https://openrouter.ai/models',
    liveModels: 2,
    lastSuccessfulSyncAt: '2026-08-23T10:00:00.000Z',
    lastAttemptedSyncAt: '2026-08-23T10:00:00.000Z',
    lastOutcome: 'ok',
    freshness: 'fresh',
    hoursSinceSuccess: 1,
    qualityScored: 2,
    modelScoreScored: 2,
    overallScoreScored: 2,
    unrated: 0,
    ...over,
  };
}

function model(over: Partial<ApiModel> = {}): ApiModel {
  return {
    providerId: 'openrouter',
    modelId: 'anthropic/claude-sonnet-4',
    displayName: 'Claude Sonnet 4',
    canonicalId: 'anthropic/claude-sonnet-4',
    vendorModelId: null,
    state: 'active',
    lifecycle: 'current',
    contextTokens: 200_000,
    maxOutputTokens: 16_000,
    inputModalities: ['text', 'image'],
    capabilities: { tools: true, reasoning: true, structured: true, attachment: true },
    ...over,
  } as ApiModel;
}

describe('matchesProviderSearch', () => {
  const openRouter = provider();
  const models = [model(), model({ modelId: 'openai/gpt-5', displayName: 'GPT-5', inputModalities: ['text'] })];

  test('matches human-facing provider and model text', () => {
    expect(matchesProviderSearch(openRouter, models, 'Hosted frontier models', 'frontier')).toBe(true);
    expect(matchesProviderSearch(openRouter, models, 'Hosted frontier models', 'sonnet')).toBe(true);
    expect(matchesProviderSearch(openRouter, models, 'Hosted frontier models', 'does-not-exist')).toBe(false);
  });

  test('supports field-qualified model and provider queries', () => {
    expect(matchesProviderSearch(openRouter, models, '', 'model:claude')).toBe(true);
    expect(matchesProviderSearch(openRouter, models, '', 'model:gemini')).toBe(false);
    expect(matchesProviderSearch(openRouter, models, '', 'provider:openrouter model:gpt-5')).toBe(true);
  });

  test('supports capability, status, and score qualifiers', () => {
    expect(matchesProviderSearch(openRouter, models, '', 'capability:vision')).toBe(true);
    expect(matchesProviderSearch(openRouter, models, '', 'capability:tools capability:reasoning')).toBe(true);
    expect(matchesProviderSearch(openRouter, models, '', 'status:fresh score:complete')).toBe(true);
    expect(matchesProviderSearch(provider({ freshness: 'stale', overallScoreScored: 1 }), models, '', 'status:fresh')).toBe(false);
    expect(matchesProviderSearch(provider({ freshness: 'stale', overallScoreScored: 1 }), models, '', 'score:incomplete')).toBe(true);
  });
});

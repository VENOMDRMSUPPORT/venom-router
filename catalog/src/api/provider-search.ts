import type { ApiModel, ApiProvider } from './client';

export type ProviderSearchField = 'provider' | 'model' | 'capability' | 'status' | 'score';

type SearchToken =
  | { field: ProviderSearchField; value: string }
  | { field: 'text'; value: string };

function normalize(value: string): string {
  return value.trim().toLowerCase();
}

function modelText(model: ApiModel): string {
  return [model.modelId, model.displayName, model.canonicalId, model.vendorModelId]
    .filter(Boolean)
    .join(' ')
    .toLowerCase();
}

function matchesCapability(model: ApiModel, value: string): boolean {
  const capability = normalize(value);
  if (capability === 'multimodal' || capability === 'vision') {
    return (model.inputModalities ?? []).some((modality) => modality !== 'text')
      || model.capabilities.attachment === true;
  }
  if (capability === 'tools' || capability === 'tool-calling') return model.capabilities.tools === true;
  if (capability === 'reasoning') return model.capabilities.reasoning === true;
  if (capability === 'structured' || capability === 'json') return model.capabilities.structured === true;
  if (capability === 'attachment' || capability === 'files') return model.capabilities.attachment === true;
  return false;
}

function matchesStatus(provider: ApiProvider, value: string): boolean {
  const status = normalize(value);
  if (status === 'fresh' || status === 'stale' || status === 'never') return provider.freshness === status;
  if (status === 'failed' || status === 'error') return provider.lastOutcome === 'failed';
  if (status === 'ok' || status === 'healthy') return provider.lastOutcome === 'ok';
  return false;
}

function matchesScore(provider: ApiProvider, value: string): boolean {
  const score = normalize(value);
  const complete = provider.liveModels > 0 && provider.overallScoreScored >= provider.liveModels;
  if (score === 'complete' || score === 'ready') return complete;
  if (score === 'incomplete' || score === 'missing') return !complete;
  return false;
}

function parseQuery(query: string): SearchToken[] {
  return normalize(query)
    .split(/\s+/)
    .filter(Boolean)
    .map((raw): SearchToken => {
      const match = /^(provider|model|capability|status|score):(.+)$/.exec(raw);
      return match
        ? { field: match[1] as ProviderSearchField, value: match[2] }
        : { field: 'text', value: raw };
    });
}

/**
 * Match a provider using human-facing metadata and its served model roster.
 *
 * This is a display/search predicate only. It never derives or changes catalog
 * facts; all score, freshness, and capability values come from the API payload.
 * Every token is an AND condition, while a plain token can match either the
 * provider or one of its models.
 */
export function matchesProviderSearch(
  provider: ApiProvider,
  models: ApiModel[],
  blurb: string,
  query: string,
): boolean {
  const tokens = parseQuery(query);
  if (tokens.length === 0) return true;

  const providerModels = models.filter((model) => model.providerId === provider.id);
  const providerText = [provider.name, provider.id, provider.rosterUrl, blurb].join(' ').toLowerCase();
  const modelsText = providerModels.map(modelText);

  return tokens.every((token) => {
    if (token.field === 'provider') return providerText.includes(token.value);
    if (token.field === 'model') return modelsText.some((text) => text.includes(token.value));
    if (token.field === 'capability') return providerModels.some((model) => matchesCapability(model, token.value));
    if (token.field === 'status') return matchesStatus(provider, token.value);
    if (token.field === 'score') return matchesScore(provider, token.value);
    return providerText.includes(token.value) || modelsText.some((text) => text.includes(token.value));
  });
}

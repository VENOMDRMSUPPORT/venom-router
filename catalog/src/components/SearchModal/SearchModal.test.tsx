import { describe, test, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SearchModal } from './SearchModal';

const mockCatalogData = {
  providers: [
    {
      id: 'openrouter',
      name: 'OpenRouter',
      rosterUrl: 'https://openrouter.ai/api/v1/models',
      liveModels: 120,
      lastSuccessfulSyncAt: null,
      lastAttemptedSyncAt: null,
      lastOutcome: 'ok' as const,
      freshness: 'fresh' as const,
      hoursSinceSuccess: 1,
      qualityScored: 80,
      unrated: 40,
    },
    {
      id: 'groq',
      name: 'Groq Cloud',
      rosterUrl: 'https://api.groq.com/openai/v1/models',
      liveModels: 15,
      lastSuccessfulSyncAt: null,
      lastAttemptedSyncAt: null,
      lastOutcome: 'ok' as const,
      freshness: 'fresh' as const,
      hoursSinceSuccess: 1,
      qualityScored: 15,
      unrated: 0,
    },
  ],
  models: [
    {
      providerId: 'openrouter',
      modelId: 'anthropic/claude-3.5-sonnet',
      canonicalId: 'anthropic/claude-3.5-sonnet',
      displayName: 'Claude 3.5 Sonnet',
      state: 'active' as const,
      contextTokens: 200000,
      maxOutputTokens: 8192,
      inputModalities: ['text', 'image'],
      capabilities: { tools: true, reasoning: true, structured: true, attachment: true },
      pricing: {
        kind: 'per_token' as const,
        inputPerMTokens: 3,
        outputPerMTokens: 15,
        referenceInPerMTokens: null,
        referenceOutPerMTokens: null,
        isFree: false,
      },
      vq: {
        value: 78,
        uncertainty: null,
        bound: null,
        evidenceLevel: 'measured' as const,
        precision: 0,
        display: '78',
        unratedReason: null,
        provenance: null,
      },
      vo: {
        value: 85,
        dimensions: { context: 80, output: 80, capabilities: 90, cost: 70 },
        missingDimensions: [],
        notApplicableDimensions: [],
        profileId: 'balanced',
      },
      catalogReady: true,
      missingFacts: [],
      conflicts: [],
      provenanceByField: {},
      qualityRank: 1,
      tiedAtRank: false,
      firstSeenAt: '2026-01-01T00:00:00Z',
      lastSeenAt: '2026-08-18T00:00:00Z',
    },
  ],
  meta: {
    methodologyVersion: 'v1',
    profileId: 'balanced',
    liveModels: 135,
    catalogReady: 135,
    needsVerification: 0,
    qualityScored: 95,
    operationalScored: 135,
    unrated: 40,
    identity: { resolvedWithEvidence: 95, resolvedWithoutEvidence: 40, unresolved: 0, ambiguousOpen: 0 },
    identityRules: { exact: 135 },
    calibration: null,
    sortContracts: {},
  },
};

vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({
    data: mockCatalogData,
    loading: false,
    error: null,
    reload: vi.fn(),
  }),
}));

describe('SearchModal', () => {
  test('renders quick navigation pages and providers when opened with empty query', () => {
    render(
      <MemoryRouter>
        <SearchModal isOpen={true} onClose={vi.fn()} />
      </MemoryRouter>
    );

    expect(screen.getByPlaceholderText(/search models, providers/i)).toBeInTheDocument();
    expect(screen.getByText('Dashboard')).toBeInTheDocument();
    expect(screen.getByText("What's New")).toBeInTheDocument();
    expect(screen.getByText('Workspace Settings')).toBeInTheDocument();
    expect(screen.getByText('OpenRouter')).toBeInTheDocument();
  });

  test('filters models and providers when typing a query', () => {
    render(
      <MemoryRouter>
        <SearchModal isOpen={true} onClose={vi.fn()} />
      </MemoryRouter>
    );

    const input = screen.getByPlaceholderText(/search models, providers/i);
    fireEvent.change(input, { target: { value: 'claude' } });

    // The official display name leads the result; the raw id is searchable but
    // no longer the title.
    expect(screen.getByText('Claude 3.5 Sonnet')).toBeInTheDocument();
    expect(screen.getByText('VQ 78')).toBeInTheDocument();
  });

  test('calls onClose when clicking ESC badge', () => {
    const onClose = vi.fn();
    render(
      <MemoryRouter>
        <SearchModal isOpen={true} onClose={onClose} />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByText('ESC'));
    expect(onClose).toHaveBeenCalled();
  });
});

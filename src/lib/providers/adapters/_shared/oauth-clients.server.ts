/* Public OAuth client credentials — same as 9router open-sse/providers/shared.js */

export const CLAUDE_OAUTH = {
  clientId: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  authorizeUrl: "https://claude.ai/oauth/authorize",
  tokenUrl: "https://platform.claude.com/v1/oauth/token",
  scopes: ["org:create_api_key", "user:profile", "user:inference"],
  refreshLeadMs: 14_400_000,
} as const;

export const ANTIGRAVITY_OAUTH_CLIENT = {
  client_id: "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com",
  client_secret: "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",
} as const;

export function antigravityClientForRedirect(redirect_uri: string) {
  return { ...ANTIGRAVITY_OAUTH_CLIENT, redirect_uri };
}

export const CLAUDE_CURATED_MODELS = [
  {
    external_id: "claude-opus-4-8",
    display_name: "Claude Opus 4.8",
    capabilities: ["chat", "tools", "vision", "reasoning"],
    context_window: 200_000,
    quality_rating: 96,
  },
  {
    external_id: "claude-opus-4-7",
    display_name: "Claude Opus 4.7",
    capabilities: ["chat", "tools", "vision", "reasoning"],
    context_window: 200_000,
    quality_rating: 95,
  },
  {
    external_id: "claude-opus-4-6",
    display_name: "Claude Opus 4.6",
    capabilities: ["chat", "tools", "vision", "reasoning"],
    context_window: 200_000,
    quality_rating: 94,
  },
  {
    external_id: "claude-sonnet-4-6",
    display_name: "Claude Sonnet 4.6",
    capabilities: ["chat", "tools", "vision", "reasoning"],
    context_window: 200_000,
    quality_rating: 85,
  },
  {
    external_id: "claude-opus-4-5-20251101",
    display_name: "Claude 4.5 Opus",
    capabilities: ["chat", "tools", "vision", "reasoning"],
    context_window: 200_000,
    quality_rating: 93,
  },
  {
    external_id: "claude-sonnet-4-5-20250929",
    display_name: "Claude 4.5 Sonnet",
    capabilities: ["chat", "tools", "vision", "reasoning"],
    context_window: 200_000,
    quality_rating: 84,
  },
  {
    external_id: "claude-haiku-4-5-20251001",
    display_name: "Claude 4.5 Haiku",
    capabilities: ["chat", "tools", "vision"],
    context_window: 200_000,
    quality_rating: 72,
  },
] as const;

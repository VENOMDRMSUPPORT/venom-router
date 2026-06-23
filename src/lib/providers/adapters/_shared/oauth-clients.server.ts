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

// Per-slug PRESENTATION metadata for the Provider Fleet (marketing copy,
// official-site links, per-provider badge variants) — the catalog's own
// descriptions are terse/technical, and the documented target UI shows
// richer copy per integration.
//
// PRESENTATION ONLY, by contract: nothing here overrides server truth.
// auth_mode / configured / connected always come from GET /providers and
// GET /accounts; an unknown slug simply falls back to the server's own
// display_name/description and the generic auth badge, so a brand-new
// provider renders honestly with zero changes to this file.

import type { Provider } from "../api/controlClient";

export interface ProviderMeta {
  /** Only present where the marketing name differs from the catalog's. */
  displayName?: string;
  siteUrl: string;
  /** The short label shown under the name on the catalog card. */
  siteLabel: string;
  /** The long-form mono auth badge on the catalog card (e.g. "OAUTH 2 · PKCE"). */
  cardBadge: string;
  description: string;
  /** Card call-to-action override (e.g. codex's "Login with ChatGPT"). */
  cta?: string;
}

export const PROVIDER_META: Record<string, ProviderMeta> = {
  "claude-code": {
    siteUrl: "https://claude.ai",
    siteLabel: "claude.ai",
    cardBadge: "OAUTH 2 · PKCE",
    description:
      "Anthropic Claude via the Claude Code OAuth flow. Sign in with your claude.ai account to access Opus, Sonnet and Haiku.",
  },
  antigravity: {
    siteUrl: "https://antigravity.google",
    siteLabel: "antigravity.google",
    cardBadge: "OAUTH 2",
    description:
      "Google Antigravity IDE — sign in with Google to access Gemini, Claude and GPT-OSS models via the Cloud Code Assist API.",
  },
  codex: {
    displayName: "OpenAI Codex / ChatGPT",
    siteUrl: "https://chatgpt.com/codex",
    siteLabel: "https://chatgpt.com/codex",
    cardBadge: "CHATGPT OAUTH",
    description: "OpenAI Codex OAuth provider for GPT-5.x coding models via the ChatGPT backend API.",
    cta: "Login with ChatGPT",
  },
  clinepass: {
    siteUrl: "https://cline.bot",
    siteLabel: "https://cline.bot",
    cardBadge: "OAUTH 2 · PKCE",
    description: "ClinePass OAuth provider for Cline extension accounts.",
  },
  "github-copilot": {
    siteUrl: "https://github.com/features/copilot",
    siteLabel: "https://github.com/features/copilot",
    cardBadge: "OAUTH 2 · PKCE",
    description: "GitHub Copilot OAuth provider for Copilot chat and embedding models.",
  },
  xai: {
    displayName: "xAI / Grok",
    siteUrl: "https://x.ai",
    siteLabel: "https://x.ai",
    cardBadge: "OAUTH + API KEY",
    description: "xAI / Grok — Grok Build OAuth (subscription) or xAI API Key (console.x.ai).",
  },
  "opencode-zen": {
    siteUrl: "https://opencode.ai",
    siteLabel: "opencode.ai",
    cardBadge: "API KEY",
    description:
      "OpenAI-compatible free gateway from OpenCode. Add your zen API key to access deepseek, nemotron and other free models.",
  },
  "agnes-ai": {
    siteUrl: "https://agnes-ai.com",
    siteLabel: "agnes-ai.com",
    cardBadge: "API KEY",
    description:
      "OpenAI-compatible free gateway from Agnes AI (Sapiens AI). Add your API key to access Agnes 2.0 Flash — 512K context, tool calling and vision — currently free.",
  },
  "gemini-cli": {
    siteUrl: "https://aistudio.google.com",
    siteLabel: "aistudio.google.com",
    cardBadge: "API KEY",
    description:
      "Free Gemini Developer API (AI Studio) gateway used by Google's official gemini-cli. Add your free AI Studio API key to access Gemini 2.x/3.x models dynamically.",
  },
  "ollama-cloud": {
    siteUrl: "https://ollama.com",
    siteLabel: "https://ollama.com",
    cardBadge: "API KEY",
    description:
      "Ollama's hosted cloud models via an OpenAI-compatible API — DeepSeek, Kimi, GLM, Qwen, GPT-OSS and more. Free plan: GPU-time based usage, session limit resets every 5h.",
  },
  "nvidia-nim": {
    siteUrl: "https://build.nvidia.com",
    siteLabel: "https://build.nvidia.com",
    cardBadge: "API KEY",
    description:
      "NVIDIA-hosted, OpenAI-compatible inference for open models (Llama, Mistral, Nemotron, Qwen and more) via NIM microservices. Free tier: no published SLA, community-supported.",
  },
};

/** This slug's presentation meta, or undefined for an unknown slug (the
 * callers below then fall back to the server's own fields). */
export function providerMeta(id: string): ProviderMeta | undefined {
  return PROVIDER_META[id];
}

/** The name to render: the marketing display name where one is declared
 * (codex, xai), the catalog's display_name everywhere else. */
export function providerDisplayName(provider: Provider): string {
  return providerMeta(provider.id)?.displayName ?? provider.display_name;
}

/** The description to render: marketing copy for known slugs, the server's
 * catalog description otherwise. */
export function providerDescription(provider: Provider): string {
  return providerMeta(provider.id)?.description ?? provider.description;
}

/** The generic long-form auth badge (the pre-meta legacy card strings) —
 * the fallback for slugs without a declared cardBadge. `custom_openai` has
 * no legacy card equivalent, so it gets an honest OpenAI-compatible label
 * instead of a wrong "API KEY". */
export function authBadgeLabel(mode: Provider["auth_mode"]): string {
  if (mode === "oauth2") return "OAUTH 2 · PKCE";
  if (mode === "api_key") return "API KEY";
  return "OPENAI COMPATIBLE";
}

/** The catalog card's mono auth badge: the per-slug variant when declared,
 * else the generic auth_mode label. */
export function cardBadgeLabel(provider: Provider): string {
  return providerMeta(provider.id)?.cardBadge ?? authBadgeLabel(provider.auth_mode);
}

/** The Active Providers row's SHORT mono auth badge — derived from the
 * server's typed auth_mode only, never from the slug. */
export function rowBadgeLabel(mode: Provider["auth_mode"]): string {
  if (mode === "oauth2") return "OAUTH";
  if (mode === "api_key") return "API KEY";
  return "OPENAI COMPATIBLE";
}

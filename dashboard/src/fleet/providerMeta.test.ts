import { describe, expect, it } from "vitest";
import type { Provider } from "../api/controlClient";
import {
  cardBadgeLabel,
  providerDescription,
  providerDisplayName,
  providerMeta,
  PROVIDER_META,
  rowBadgeLabel,
} from "./providerMeta";
import { PROVIDER_LOGO_SLUGS } from "./providerLogos";

function provider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: "some-unknown-provider",
    display_name: "Server Display Name",
    description: "The server's own catalog description.",
    auth_mode: "api_key",
    funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null },
    capabilities: [],
    configured: true,
    ...overrides,
  };
}

describe("providerMeta — unknown-slug fallback (server truth wins)", () => {
  it("returns undefined for an unknown slug", () => {
    expect(providerMeta("some-unknown-provider")).toBeUndefined();
  });

  it("falls back to the server's display_name and description", () => {
    const p = provider();
    expect(providerDisplayName(p)).toBe("Server Display Name");
    expect(providerDescription(p)).toBe("The server's own catalog description.");
  });

  it("falls back to the generic auth badge, including the honest custom_openai label", () => {
    expect(cardBadgeLabel(provider({ auth_mode: "api_key" }))).toBe("API KEY");
    expect(cardBadgeLabel(provider({ auth_mode: "oauth2" }))).toBe("OAUTH 2 · PKCE");
    expect(cardBadgeLabel(provider({ auth_mode: "custom_openai" }))).toBe("OPENAI COMPATIBLE");
  });
});

describe("providerMeta — known slugs", () => {
  it("overrides the display name ONLY where the doc declares one", () => {
    expect(providerDisplayName(provider({ id: "codex", display_name: "Codex" }))).toBe("OpenAI Codex / ChatGPT");
    expect(providerDisplayName(provider({ id: "xai", display_name: "xAI" }))).toBe("xAI / Grok");
    // claude-code has no displayName override — the server's name stands.
    expect(providerDisplayName(provider({ id: "claude-code", display_name: "Claude Code" }))).toBe("Claude Code");
  });

  it("declares the per-slug card badge variants", () => {
    expect(cardBadgeLabel(provider({ id: "claude-code", auth_mode: "oauth2" }))).toBe("OAUTH 2 · PKCE");
    expect(cardBadgeLabel(provider({ id: "antigravity", auth_mode: "oauth2" }))).toBe("OAUTH 2");
    expect(cardBadgeLabel(provider({ id: "codex", auth_mode: "oauth2" }))).toBe("CHATGPT OAUTH");
    expect(cardBadgeLabel(provider({ id: "xai", auth_mode: "oauth2" }))).toBe("OAUTH + API KEY");
    expect(cardBadgeLabel(provider({ id: "opencode-zen", auth_mode: "api_key" }))).toBe("API KEY");
  });

  it("declares codex's own call-to-action", () => {
    expect(providerMeta("codex")?.cta).toBe("Login with ChatGPT");
  });

  it("ships meta for every provider that ships a logo (the documented eleven)", () => {
    for (const slug of PROVIDER_LOGO_SLUGS) {
      expect(PROVIDER_META[slug], `meta for ${slug}`).toBeDefined();
      expect(PROVIDER_META[slug].siteUrl).toMatch(/^https:\/\//);
      expect(PROVIDER_META[slug].description.length).toBeGreaterThan(0);
    }
  });
});

describe("rowBadgeLabel — typed auth mode only, no slug logic", () => {
  it("maps the three auth modes to the short row badges", () => {
    expect(rowBadgeLabel("oauth2")).toBe("OAUTH");
    expect(rowBadgeLabel("api_key")).toBe("API KEY");
    expect(rowBadgeLabel("custom_openai")).toBe("OPENAI COMPATIBLE");
  });
});

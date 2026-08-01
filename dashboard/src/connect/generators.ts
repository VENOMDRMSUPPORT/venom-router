// Client-setup config generators (P6-UI-011, docs/08 §8 "the documented recipes").
//
// ─── THE TWO RULES THIS MODULE EXISTS TO ENFORCE ────────────────────────────
//
// 1. EXACTLY ONE GENERATOR PER CONFIG SHAPE. Six named targets share three real
//    shapes: an OpenAI-compatible base URL + key pair (Codex, Cursor, Cline,
//    Continue, the generic SDK), Anthropic's own env names (Claude Code), and a
//    runnable curl. Writing a second generator for a shape that already has one is
//    exactly the "divergent duplicates" defect this card names — the two copies
//    drift, and one of them starts telling owners something false. The structure
//    below makes that impossible to do by accident: a target NAMES its shape, and
//    the shape owns the only function that renders it. `generatorsByShape` is the
//    single registry, and a test asserts every target resolves into it.
//
// 2. THE KEY IS NEVER IN THE OUTPUT BY DEFAULT. Every generator takes an
//    `apiKey` that is `null` unless the owner explicitly asked to include it, and
//    with null it emits a PLACEHOLDER. A Venom key is shown once at creation
//    (09 §3.11) and cannot be re-read, so a key baked into copy-paste text is a
//    secret the owner may not realise they are about to paste into a file, a chat
//    message, or a screenshot. Opt-in only, and even then the caller keeps it in
//    memory — nothing here writes a file or touches storage.

/** The tier names, which are the model ids clients address (05 §1). Derived
 * nowhere else: these three ARE the product surface. */
export const TIER_MODEL_IDS = ["venom/lite", "venom/pro", "venom/max"] as const;

/** The placeholder that stands in for the key whenever it is not included. It is
 * deliberately obvious, so a pasted config that was never filled in fails loudly
 * instead of authenticating as something unexpected. */
export const KEY_PLACEHOLDER = "<YOUR_VENOM_API_KEY>";

/** The distinct CONFIG SHAPES. A shape is a rendering; a target is a product that
 * uses one. */
export type ConfigShape = "openai-compatible-env" | "anthropic-env" | "curl";

/** What every generator needs. `apiKey` is null unless the owner opted in. */
export interface GeneratorInput {
  /** The router's public data-plane base, e.g. `http://127.0.0.1:8081/v1`. */
  baseUrl: string;
  /** Null ⇒ emit KEY_PLACEHOLDER. Never defaulted to a real key. */
  apiKey: string | null;
}

export interface GeneratedConfig {
  /** How the owner should use the text (a shell block, a JSON file, …). */
  language: "shell" | "json";
  filename?: string;
  text: string;
}

/** Resolves the key to emit — the ONE place the opt-in is honoured, so no
 * generator can forget it. */
function keyFor(input: GeneratorInput): string {
  return input.apiKey ?? KEY_PLACEHOLDER;
}

/**
 * The OpenAI-compatible shape: a base URL and an API key under OPENAI_* names.
 *
 * Codex, Cursor, Cline, Continue and the generic OpenAI SDK all read exactly this
 * pair, so they share this one generator rather than each getting a near-copy.
 */
function openAICompatibleEnv(input: GeneratorInput): GeneratedConfig {
  return {
    language: "shell",
    text: [
      `export OPENAI_BASE_URL="${input.baseUrl}"`,
      `export OPENAI_API_KEY="${keyFor(input)}"`,
      `# Address a tier by its model id:`,
      ...TIER_MODEL_IDS.map((id) => `#   ${id}`),
    ].join("\n"),
  };
}

/**
 * The Anthropic shape: Claude Code reads ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN
 * plus per-slot model envs, so it is a genuinely different rendering rather than a
 * variant of the OpenAI one.
 */
function anthropicEnv(input: GeneratorInput): GeneratedConfig {
  return {
    language: "shell",
    text: [
      `export ANTHROPIC_BASE_URL="${input.baseUrl}"`,
      `export ANTHROPIC_AUTH_TOKEN="${keyFor(input)}"`,
      `export ANTHROPIC_MODEL="venom/pro"`,
      `export ANTHROPIC_SMALL_FAST_MODEL="venom/lite"`,
      `# Raise ANTHROPIC_MODEL to venom/max for the largest context and thinking budget.`,
    ].join("\n"),
  };
}

/** A runnable request, for verifying the router answers before wiring a client. */
function curlRequest(input: GeneratorInput): GeneratedConfig {
  return {
    language: "shell",
    text: [
      `curl ${input.baseUrl}/chat/completions \\`,
      `  -H "Authorization: Bearer ${keyFor(input)}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '{"model":"venom/lite","messages":[{"role":"user","content":"ping"}]}'`,
    ].join("\n"),
  };
}

/**
 * THE registry: one generator per shape, and only one.
 *
 * A shape appears here exactly once. Adding a target never adds a function — it
 * points at a shape that already exists, or introduces a shape that genuinely
 * renders differently.
 */
export const generatorsByShape: Record<ConfigShape, (input: GeneratorInput) => GeneratedConfig> = {
  "openai-compatible-env": openAICompatibleEnv,
  "anthropic-env": anthropicEnv,
  curl: curlRequest,
};

export interface ClientTarget {
  id: string;
  label: string;
  /** Which SHAPE renders this target. Targets sharing a shape share its generator. */
  shape: ConfigShape;
  /** Why this target uses this shape — so a future reader can tell a deliberate
   * share from an oversight. */
  note: string;
}

/** The client-setup catalog. Six targets, three shapes. */
export const CLIENT_TARGETS: readonly ClientTarget[] = [
  {
    id: "claude-code",
    label: "Claude Code",
    shape: "anthropic-env",
    note: "Reads ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN and its own per-slot model envs.",
  },
  {
    id: "codex",
    label: "Codex",
    shape: "openai-compatible-env",
    note: "OpenAI-compatible: base URL + key.",
  },
  {
    id: "cursor",
    label: "Cursor",
    shape: "openai-compatible-env",
    note: "OpenAI-compatible: base URL + key.",
  },
  {
    id: "cline",
    label: "Cline",
    shape: "openai-compatible-env",
    note: "OpenAI-compatible: base URL + key.",
  },
  {
    id: "continue",
    label: "Continue",
    shape: "openai-compatible-env",
    note: "OpenAI-compatible: base URL + key.",
  },
  {
    id: "openai-sdk",
    label: "Generic OpenAI SDK",
    shape: "openai-compatible-env",
    note: "OpenAI-compatible: base URL + key.",
  },
  {
    id: "curl",
    label: "curl (verify the router answers)",
    shape: "curl",
    note: "A runnable request, for checking the router before wiring a client.",
  },
];

/** Generates one target's config through its shape's single generator. */
export function generateForTarget(target: ClientTarget, input: GeneratorInput): GeneratedConfig {
  return generatorsByShape[target.shape](input);
}

/**
 * The router's public data-plane base URL.
 *
 * Derived from the settings `effective_config` when it is known: `data_plane_bind`
 * when a separate public listener is configured, otherwise the control `bind`,
 * which is where /v1 lives in the default single-listener install (01 §6b). The
 * fallback is used only when settings could not be read at all — and it is the
 * documented default rather than a guess.
 *
 * A `0.0.0.0` bind is rewritten to `127.0.0.1` for the DISPLAYED url: 0.0.0.0 is a
 * listen-on-everything address, not an address a client can dial.
 */
export function dataPlaneBaseUrl(effective: { bind: string; data_plane_bind: string | null } | null): string {
  const DEFAULT_BIND = "127.0.0.1:8081";
  const bind = effective === null ? DEFAULT_BIND : (effective.data_plane_bind ?? effective.bind);
  const dialable = bind.replace(/^0\.0\.0\.0:/, "127.0.0.1:").replace(/^\[::\]:/, "127.0.0.1:");
  return `http://${dialable}/v1`;
}

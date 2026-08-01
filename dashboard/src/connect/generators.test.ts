import { describe, expect, it } from "vitest";
import {
  CLIENT_TARGETS,
  dataPlaneBaseUrl,
  generateForTarget,
  generatorsByShape,
  KEY_PLACEHOLDER,
  TIER_MODEL_IDS,
  type ConfigShape,
} from "./generators";

const BASE = "http://127.0.0.1:8081/v1";
const REAL_KEY = "vk_live_9f3c1d77aa2b4e6c8051d3f4b7e2a9c6";

describe("generators — one generator per config shape", () => {
  // THE structural assertion the card asks for. It is not a comment: the registry
  // is keyed BY shape, so a second generator for an existing shape cannot exist
  // without either replacing the first (caught by the target tests) or adding a
  // shape no target uses (caught here).
  it("registers exactly one generator per shape, and every shape is used by a target", () => {
    const registered = Object.keys(generatorsByShape) as ConfigShape[];
    const used = new Set(CLIENT_TARGETS.map((t) => t.shape));

    // No orphan generator: a shape nobody renders is a divergent duplicate waiting
    // to be picked up by mistake.
    for (const shape of registered) {
      expect(used.has(shape), `shape ${shape} has a generator but no target uses it`).toBe(true);
    }
    // No target pointing at a shape with no generator.
    for (const shape of used) {
      expect(typeof generatorsByShape[shape], `shape ${shape} has no generator`).toBe("function");
    }
    // And the registry has no duplicate keys by construction (it is an object), so
    // the count of generators equals the count of DISTINCT shapes.
    expect(registered.length).toBe(used.size);
  });

  it("gives targets that share a shape the IDENTICAL generator function", () => {
    // Codex / Cursor / Cline / Continue / the generic SDK are all one shape. If any
    // of them had been forked into its own generator, these would differ.
    const openAITargets = CLIENT_TARGETS.filter((t) => t.shape === "openai-compatible-env");
    expect(openAITargets.length).toBeGreaterThan(1);

    const fns = new Set(openAITargets.map((t) => generatorsByShape[t.shape]));
    expect(fns.size, "targets sharing a shape must share ONE function").toBe(1);

    // And they therefore produce byte-identical output for the same input.
    const outputs = new Set(
      openAITargets.map((t) => generateForTarget(t, { baseUrl: BASE, apiKey: null }).text),
    );
    expect(outputs.size, "one shape must render one text").toBe(1);
  });

  it("renders genuinely different shapes differently", () => {
    // The one-per-shape rule would be vacuous if every shape rendered the same
    // thing. Claude Code's env names really are distinct from the OpenAI ones.
    const anthropic = generatorsByShape["anthropic-env"]({ baseUrl: BASE, apiKey: null }).text;
    const openai = generatorsByShape["openai-compatible-env"]({ baseUrl: BASE, apiKey: null }).text;
    expect(anthropic).not.toBe(openai);
    expect(anthropic).toContain("ANTHROPIC_BASE_URL");
    expect(openai).toContain("OPENAI_BASE_URL");
    expect(openai).not.toContain("ANTHROPIC_");
  });
});

describe("generators — the key is never in the output by default", () => {
  it("emits the placeholder, not a key, for every target when none was opted into", () => {
    for (const target of CLIENT_TARGETS) {
      const out = generateForTarget(target, { baseUrl: BASE, apiKey: null });
      expect(out.text, `${target.id} must carry the placeholder`).toContain(KEY_PLACEHOLDER);
    }
  });

  it("never leaks a real key into generated output when the owner did not opt in", () => {
    // The load-bearing case: the surface HAS a key in memory (the owner just
    // created one) and the generated text must still not contain it.
    for (const target of CLIENT_TARGETS) {
      const out = generateForTarget(target, { baseUrl: BASE, apiKey: null });
      expect(out.text, `${target.id} leaked the key`).not.toContain(REAL_KEY);
      expect(out.text).not.toContain("vk_live_");
    }
  });

  it("includes the key ONLY when explicitly asked", () => {
    // The other direction, so the test above cannot pass by never emitting a key
    // at all.
    for (const target of CLIENT_TARGETS) {
      const out = generateForTarget(target, { baseUrl: BASE, apiKey: REAL_KEY });
      expect(out.text, `${target.id} should carry the opted-in key`).toContain(REAL_KEY);
      expect(out.text).not.toContain(KEY_PLACEHOLDER);
    }
  });
});

describe("generators — content", () => {
  it("addresses the router at the given base URL for every target", () => {
    for (const target of CLIENT_TARGETS) {
      const out = generateForTarget(target, { baseUrl: BASE, apiKey: null });
      expect(out.text, `${target.id} must name the base URL`).toContain(BASE);
    }
  });

  it("names the three tier model ids", () => {
    const openai = generatorsByShape["openai-compatible-env"]({ baseUrl: BASE, apiKey: null }).text;
    for (const id of TIER_MODEL_IDS) {
      expect(openai, `${id} must appear`).toContain(id);
    }
    // Claude Code addresses tiers through its model envs.
    const anthropic = generatorsByShape["anthropic-env"]({ baseUrl: BASE, apiKey: null }).text;
    expect(anthropic).toContain("venom/pro");
    expect(anthropic).toContain("venom/lite");
    expect(anthropic).toContain("venom/max");
  });

  it("produces shell-shaped output that parses as complete lines", () => {
    for (const target of CLIENT_TARGETS) {
      const out = generateForTarget(target, { baseUrl: BASE, apiKey: null });
      expect(out.language).toBe("shell");
      expect(out.text.trim().length).toBeGreaterThan(0);
      // No unterminated quote: an odd number of double quotes means a broken paste.
      const quotes = (out.text.match(/"/g) ?? []).length;
      expect(quotes % 2, `${target.id} has an unbalanced quote`).toBe(0);
    }
  });

  it("covers the six named targets the card lists", () => {
    const ids = CLIENT_TARGETS.map((t) => t.id);
    for (const required of ["claude-code", "codex", "cursor", "cline", "continue", "openai-sdk"]) {
      expect(ids, `${required} must be in the catalog`).toContain(required);
    }
  });
});

describe("dataPlaneBaseUrl", () => {
  it("prefers the dedicated data-plane bind when one is configured", () => {
    expect(dataPlaneBaseUrl({ bind: "127.0.0.1:8081", data_plane_bind: "127.0.0.1:9090" })).toBe(
      "http://127.0.0.1:9090/v1",
    );
  });

  it("falls back to the control bind, where /v1 lives in the default install", () => {
    expect(dataPlaneBaseUrl({ bind: "127.0.0.1:8081", data_plane_bind: null })).toBe(
      "http://127.0.0.1:8081/v1",
    );
  });

  it("rewrites a listen-on-everything bind to a dialable loopback address", () => {
    // 0.0.0.0 is where the server LISTENS, not an address a client can dial.
    expect(dataPlaneBaseUrl({ bind: "0.0.0.0:8081", data_plane_bind: null })).toBe(
      "http://127.0.0.1:8081/v1",
    );
  });

  it("uses the documented default only when settings could not be read", () => {
    expect(dataPlaneBaseUrl(null)).toBe("http://127.0.0.1:8081/v1");
  });

  it("honours a non-default port rather than hardcoding 8081", () => {
    // The whole point of reading effective_config: an owner who moved the port must
    // be given their port.
    expect(dataPlaneBaseUrl({ bind: "127.0.0.1:7000", data_plane_bind: null })).toContain("7000");
    expect(dataPlaneBaseUrl({ bind: "127.0.0.1:7000", data_plane_bind: null })).not.toContain("8081");
  });
});

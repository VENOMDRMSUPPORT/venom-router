// Proves the shared no-secret-rendering canary actually BITES.
//
// A canary nobody has watched go red is not evidence — it is a comment with a
// function call in front of it. Every test below is one of two kinds:
//
//   RED   a deliberately leaky fixture the canary MUST reject, and
//   GREEN a lookalike it must NOT reject.
//
// The GREEN cases matter as much as the RED ones. A canary that fires on the
// product's own correct behaviour (the non-secret 4-char key prefix the API
// Keys surface is SUPPOSED to render) gets disabled by the first developer it
// blocks, and then the RED cases protect nothing.

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import {
  assertNoSecretsRendered,
  SENTINELS,
  allSentinels,
  type DeclaredSecret,
} from "./noSecrets";
import { KEY_PREFIX_FIXTURE } from "../../tests/e2e/fixtures";

afterEach(cleanup);

/** Renders `node` and returns its container, the shape the canary scans. */
function renderContainer(node: React.ReactElement): HTMLElement {
  return render(node).container;
}

describe("no-secret-rendering canary — structural layer", () => {
  it("goes RED when a raw vk_live_ key is rendered as text", () => {
    const container = renderContainer(<p>Your new key is {SENTINELS.rawVenomKey}</p>);
    expect(() => assertNoSecretsRendered(container)).toThrow(/raw Venom API key/);
  });

  it("goes RED when a raw vk_live_ key hides in an attribute rather than in text", () => {
    // textContent is empty here: an attribute-only scan is the difference
    // between catching this and shipping it.
    const container = renderContainer(<span title={SENTINELS.rawVenomKey} />);
    expect(container.textContent).toBe("");
    expect(() => assertNoSecretsRendered(container)).toThrow(/raw Venom API key/);
  });

  it("goes RED when a provider sk- credential is rendered", () => {
    const container = renderContainer(<code>{SENTINELS.providerCredential}</code>);
    expect(() => assertNoSecretsRendered(container)).toThrow(/provider API key/);
  });

  it("goes RED when an Authorization bearer token is rendered", () => {
    const container = renderContainer(<pre>Authorization: Bearer abcdef0123456789abcdef</pre>);
    expect(() => assertNoSecretsRendered(container)).toThrow(/bearer token/i);
  });

  it("stays GREEN on the non-secret 4-char key prefix the API Keys surface is supposed to render", () => {
    // THE BOUNDARY TEST. If this ever goes red, the canary has started banning
    // the brand (`vk_live_`) instead of the entropy, and it is about to be
    // deleted by whoever it blocks. 4 hex chars is 16 bits, kept on purpose.
    const container = renderContainer(<span>{KEY_PREFIX_FIXTURE}</span>);
    expect(KEY_PREFIX_FIXTURE).toMatch(/^vk_live_[0-9a-f]{4}$/);
    expect(() => assertNoSecretsRendered(container)).not.toThrow();
  });

  it("stays GREEN on ordinary UI copy", () => {
    const container = renderContainer(
      <div>
        <h1>API Keys</h1>
        <p>Gateway keys for the chat completions surface.</p>
      </div>,
    );
    expect(() => assertNoSecretsRendered(container)).not.toThrow();
  });
});

describe("no-secret-rendering canary — declared layer", () => {
  const declared: DeclaredSecret[] = [{ label: "owner password", value: SENTINELS.ownerPassword }];

  it("goes RED when a declared secret is rendered as text", () => {
    const container = renderContainer(<p>{SENTINELS.ownerPassword}</p>);
    expect(() => assertNoSecretsRendered(container, declared)).toThrow(/owner password/);
  });

  it("goes RED when a declared secret sits in a form control's LIVE value", () => {
    // `value` is a property once React has rendered a controlled input; it is
    // not readable from the attribute map. This is the case a naive
    // attribute-plus-textContent scan misses, and it is the single most
    // likely place an owner password actually is.
    const container = renderContainer(<input readOnly value={SENTINELS.ownerPassword} aria-label="pw" />);
    expect(() => assertNoSecretsRendered(container, declared)).toThrow(/owner password/);
  });

  it("stays GREEN on the same DOM when the value is NOT declared", () => {
    // Proves the declared layer is doing the work here, not the structural
    // one — the password has no recognisable shape, so undeclared it is
    // invisible. This is why journeys declare their sentinels explicitly.
    const container = renderContainer(<p>{SENTINELS.ownerPassword}</p>);
    expect(() => assertNoSecretsRendered(container)).not.toThrow();
  });

  it("reports EVERY leaked secret, not just the first", () => {
    const container = renderContainer(
      <div>
        <p>{SENTINELS.ownerPassword}</p>
        <p>{SENTINELS.promptBody}</p>
      </div>,
    );
    let message = "";
    try {
      assertNoSecretsRendered(container, allSentinels());
    } catch (err) {
      message = err instanceof Error ? err.message : String(err);
    }
    expect(message).toContain("owner password");
    expect(message).toContain("prompt body");
  });

  it("refuses a declared value too short to be a safe substring search", () => {
    // Fail LOUDLY rather than quietly performing a check that would collide
    // with ordinary copy — a canary that cries wolf gets switched off.
    const container = renderContainer(<p>nothing secret here</p>);
    expect(() => assertNoSecretsRendered(container, [{ label: "too short", value: "abc" }])).toThrow(
      /at least 8 chars/,
    );
  });
});

// P6-TEST-001's shared no-secret-rendering canary.
//
// ONE canary, applied across every step of the jsdom flow journeys (and
// reused by the Playwright critical flows through its exported patterns), so
// there is a single definition of "a secret reached the DOM" rather than a
// per-surface hand-rolled assertion that each new surface can quietly forget.
//
// It checks TWO independent layers, because each catches what the other
// cannot:
//
//   1. STRUCTURAL patterns (always on, no declaration needed). These fire on
//      the SHAPE of a secret, so they catch a leak nobody anticipated —
//      including one introduced by a surface written after this file. This is
//      the layer that keeps working when a future unit forgets this canary
//      exists.
//
//   2. DECLARED values (opt-in per call). The caller names the exact sentinel
//      values its fixtures seeded — the owner password, the provider
//      credential, the prompt and response bodies, the account external id.
//      Structural matching cannot recognise these (a password looks like any
//      other string), so they must be declared to be checked.
//
// Both layers search the rendered text AND every attribute value AND live
// form-control values. A secret parked in a `title`, a `value=`, a `href`, or
// a `data-*` attribute has still leaked to anyone with devtools, a screen
// reader, or a screenshot — textContent alone would miss all four.

/** A structural secret shape, checked on every call without declaration. */
export interface SecretPattern {
  /** Human-readable name, used in the failure message. */
  readonly label: string;
  readonly pattern: RegExp;
}

/**
 * THE RAW-KEY BOUNDARY, and the one subtlety in this file.
 *
 * A raw Venom key is `vk_live_` + 64 hex chars (internal/httpapi/keys.go:
 * vkRawEntropyBytes = 32, hex-encoded). The API-key surface, however,
 * LEGITIMATELY renders `key_prefix` — `vk_live_` + exactly 4 hex chars
 * (keyPrefixFragmentLen = 4), which is non-secret BY DESIGN: 16 bits kept for
 * telling two keys apart, 240 bits withheld.
 *
 * So banning the literal `vk_live_` would fire on the product's own correct
 * behaviour, and the canary would be deleted within a week. The rule that
 * actually matters is the ENTROPY boundary, not the brand: 8 or more hex chars
 * after the prefix is strictly more than the display fragment can ever be, and
 * is therefore a leak. 4 passes; 64 fails. Nothing in between is reachable —
 * the server emits only those two lengths.
 */
export const RAW_VENOM_KEY_PATTERN: SecretPattern = {
  label: "raw Venom API key (vk_live_ + >=8 hex chars; the non-secret display prefix is only 4)",
  pattern: /vk_live_[0-9a-f]{8,}/i,
};

/** Structural shapes that must never appear in a rendered surface. */
export const SECRET_PATTERNS: readonly SecretPattern[] = [
  RAW_VENOM_KEY_PATTERN,
  {
    // Provider API keys (opencode-zen and every OpenAI-compatible provider)
    // are `sk-` + a long random tail. The dashboard has no legitimate reason
    // to render one: enrollment posts it and never reads it back, and no read
    // projection carries credential material at all.
    label: "provider API key (sk-...)",
    pattern: /\bsk-[A-Za-z0-9_-]{16,}/,
  },
  {
    label: "HTTP Authorization bearer token",
    pattern: /\bBearer\s+[A-Za-z0-9._-]{16,}/i,
  },
  {
    // A PEM block reaching the DOM is a private key or certificate leak.
    label: "PEM-encoded private key block",
    pattern: /-----BEGIN [A-Z ]*PRIVATE KEY-----/,
  },
];

/**
 * A caller-declared secret value that must not appear in the surface under
 * test.
 *
 * `value` must be a DISTINCTIVE sentinel (see SENTINELS below), not a natural
 * word: this is a substring search, so declaring a short or common string
 * would make the canary fire on unrelated copy and get it disabled.
 */
export interface DeclaredSecret {
  readonly label: string;
  readonly value: string;
}

/** The shortest declared value the canary will accept. Below this, a
 * substring search is more likely to collide with ordinary UI copy than to
 * catch a real leak, and a canary that cries wolf is a canary that gets
 * deleted. Callers get a loud error rather than a silently weak check. */
const MIN_DECLARED_SECRET_LENGTH = 8;

/**
 * The sentinel secret values every flow fixture seeds. Centralised so the
 * fixtures and the canary cannot drift apart — a fixture that seeds a secret
 * the canary does not know about is a hole, and a canary watching for a value
 * no fixture seeds is theatre.
 *
 * Each is deliberately unmistakable in a failure message and impossible to
 * produce by accident.
 */
export const SENTINELS = {
  ownerPassword: "canary-owner-password-8f2a1c",
  providerCredential: "sk-canaryproviderkey00000000000000",
  rawVenomKey: "vk_live_" + "c0ffee".padEnd(64, "0"),
  promptBody: "canary-prompt-body-do-not-render-4b91",
  responseBody: "canary-response-body-do-not-render-77de",
  accountExternalID: "canary-external-id-acct-6d3e",
} as const;

/** The full declared set for a surface that must show none of the above —
 * the default for every surface except the ones that legitimately own a
 * value (the Playground renders its own prompt and response; the create-key
 * dialog shows the raw key exactly once). */
export function allSentinels(): DeclaredSecret[] {
  return [
    { label: "owner password", value: SENTINELS.ownerPassword },
    { label: "provider credential", value: SENTINELS.providerCredential },
    { label: "raw Venom API key", value: SENTINELS.rawVenomKey },
    { label: "prompt body", value: SENTINELS.promptBody },
    { label: "response body", value: SENTINELS.responseBody },
    { label: "account external id", value: SENTINELS.accountExternalID },
  ];
}

/** Every string a viewer could extract from `root`: its text, every
 * attribute value on every element, and the live value of every form
 * control (which is a PROPERTY — it does not appear as an attribute once the
 * user has typed, so an attribute-only scan misses exactly the case where a
 * password is sitting in an input). */
function harvestStrings(root: Element): string[] {
  const found: string[] = [];

  const text = root.textContent;
  if (text !== null && text !== "") found.push(text);

  const elements: Element[] = [root, ...Array.from(root.querySelectorAll("*"))];
  for (const el of elements) {
    for (const attr of Array.from(el.attributes)) {
      if (attr.value !== "") found.push(attr.value);
    }
    // `value` is a property on these three; reading it off the attribute map
    // above would return the ORIGINAL value, not what is currently rendered.
    if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) {
      if (el.value !== "") found.push(el.value);
    }
  }

  return found;
}

/** Trims a haystack down to a readable excerpt centred on the match, so a
 * failure names WHERE the secret surfaced rather than dumping the DOM. */
function excerpt(haystack: string, index: number, matchLength: number): string {
  const start = Math.max(0, index - 40);
  const end = Math.min(haystack.length, index + matchLength + 40);
  return `${start > 0 ? "..." : ""}${haystack.slice(start, end)}${end < haystack.length ? "..." : ""}`;
}

/**
 * Asserts that no secret — structural or declared — appears anywhere in
 * `root`. Throws with the offending value's LABEL and an excerpt of where it
 * surfaced; never echoes the secret itself beyond that excerpt, which is
 * already in the DOM the developer is looking at.
 *
 * @param root     the rendered container to scan.
 * @param declared caller-named sentinel values that must not appear here.
 *                 Defaults to none — the structural layer still runs.
 */
export function assertNoSecretsRendered(root: Element, declared: readonly DeclaredSecret[] = []): void {
  for (const secret of declared) {
    if (secret.value.length < MIN_DECLARED_SECRET_LENGTH) {
      throw new Error(
        `assertNoSecretsRendered: declared secret ${JSON.stringify(secret.label)} is only ` +
          `${secret.value.length} chars. Declare a distinctive sentinel of at least ` +
          `${MIN_DECLARED_SECRET_LENGTH} chars — a short value makes this a substring search ` +
          `that collides with ordinary UI copy.`,
      );
    }
  }

  const haystacks = harvestStrings(root);
  const violations: string[] = [];

  for (const haystack of haystacks) {
    for (const { label, pattern } of SECRET_PATTERNS) {
      const match = pattern.exec(haystack);
      if (match !== null) {
        violations.push(`- [${label}] surfaced in: ${excerpt(haystack, match.index, match[0].length)}`);
      }
    }
    for (const { label, value } of declared) {
      const index = haystack.indexOf(value);
      if (index !== -1) {
        violations.push(`- [${label}] surfaced in: ${excerpt(haystack, index, value.length)}`);
      }
    }
  }

  if (violations.length > 0) {
    // Deduplicate: the same leak shows up once in textContent and again in
    // whichever attribute carries it, which is noise, not two findings.
    const unique = Array.from(new Set(violations));
    throw new Error(
      `no-secret-rendering canary: ${unique.length} secret(s) reached the rendered DOM:\n${unique.join("\n")}`,
    );
  }
}

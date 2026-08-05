import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { EffectiveOffering, ModelGroup, OfferingCapability } from "../api/controlClient";
import ModelsSurface from "./ModelsSurface";

const CSRF_TOKEN = "models-csrf-token";
const MODELS_URL = "GET /api/control/v1/models?limit=200";
const CENSUS_URL = "GET /api/control/v1/certifications/review";

function capability(overrides: Partial<OfferingCapability> = {}): OfferingCapability {
  return {
    operation: "chat",
    effective: true,
    state: "certified",
    truth: "supported",
    routable: true,
    provenance: "probed",
    ...overrides,
  };
}

function offering(overrides: Partial<EffectiveOffering> = {}): EffectiveOffering {
  return {
    account_id: "acct-1",
    provider_id: "opencode-zen",
    provider_model_id: "zen-chat-1",
    display_name: "Zen Chat 1",
    availability: "available",
    effective_context_tokens: 262144,
    // A real, valid ContextProvenance value (internal/models.ContextNative) —
    // the OLD default here was "probe", which the backend never actually
    // serializes (the enum is unknown/native/provider_cap). That fictional
    // value happened to match an equally fictional check in ModelsSurface
    // (`=== "probe"`), so the two masked each other and "verified" was never
    // exercised against a value the API can really send.
    context_provenance: "native",
    capabilities: [capability()],
    quality_score: 0.82,
    quality_known: true,
    cost: {
      is_free: true,
      source: "provider_policy",
      conflict: false,
      confidence: 0.9,
      exact_identity_match: true,
      stale: false,
    },
    classification: "text",
    tiers: { lite: { eligible: true, stale: false, penalty: false } },
    ...overrides,
  };
}

/** The default group mirrors a REAL backend state, which the old default did
 * not: `models.quality_rating` is the 0-100 column (04 §3) and an offering's
 * `quality_score` is that same rating / 100 (models.QualityScore). Pairing
 * `quality_rating: 0.82` with `quality_score: 0.82` — as this fixture used to
 * — is a state the backend cannot produce, and it is exactly what hid the
 * write-side scale bug from these tests. 82 / 0.82 is the honest pair. */
function group(overrides: Partial<ModelGroup> = {}): ModelGroup {
  return {
    model_id: "model-zen-chat",
    display_name: "Zen Chat",
    native_context_tokens: 262144,
    quality_rating: 82,
    latest_benchmark: { finished_at: "2026-08-04T09:30:00Z", requests: 3, successes: 3 },
    offerings: [offering()],
    ...overrides,
  };
}

/** The census payload the review banner reads. Defaults to an all-clear so
 * model-catalog tests are not coupled to the banner's own behaviour. */
function censusBody(
  byReason: { reason: string; count: number }[] = [
    { reason: "capability_not_certified", count: 0 },
  ],
) {
  return {
    data: {
      scanned: 1,
      limit: 50,
      truncated: false,
      evaluated_reasons: ["capability_not_certified"],
      not_evaluated_reasons: [
        "identity_unresolved",
        "context_unverified",
        "funding_unknown",
        "no_healthy_account",
        "quota_exhausted",
        "quota_insufficient",
        "cooling_down",
      ],
      by_reason: byReason,
    },
  };
}

function mockModels(
  groups: ModelGroup[],
  extra: Record<string, () => Response> = {},
): ReturnType<typeof createFetchMock> {
  const mock = createFetchMock({
    [MODELS_URL]: () => jsonResponse(200, { data: groups }),
    [CENSUS_URL]: () => jsonResponse(200, censusBody()),
    ...extra,
  });
  vi.stubGlobal("fetch", mock);
  return mock;
}

function renderSurface() {
  return render(<ModelsSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
}

/** Expands the given model group's row so its offerings render. */
async function expandGroup(modelId: string): Promise<void> {
  const toggle = await screen.findByTestId(`model-group-toggle-${modelId}`);
  fireEvent.click(toggle);
  await screen.findByTestId(`model-offerings-${modelId}`);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ModelsSurface — catalog", () => {
  it("lists model groups and reveals their offerings on expand", async () => {
    mockModels([group()]);
    renderSurface();

    await screen.findByText("Zen Chat");
    // Offerings are collapsed until asked for.
    expect(screen.queryByTestId("model-offerings-model-zen-chat")).toBeNull();

    await expandGroup("model-zen-chat");
    const offerings = screen.getByTestId("model-offerings-model-zen-chat");
    expect(offerings.textContent ?? "").toContain("zen-chat-1");
  });
});

describe("ModelsSurface — the certification conjunction", () => {
  // THE most important assertion of P6-UI-002. 04 §5 defines routability as a
  // CONJUNCTION: state `certified` AND truth `supported`. A chip that read only
  // the state would call this offering routable, and the operator would wait
  // forever for traffic the router will never send it.
  it("renders certified + unknown truth as NOT routable", async () => {
    mockModels([
      group({
        model_id: "model-conj",
        display_name: "Conjunction Model",
        offerings: [
          offering({
            provider_model_id: "conj-1",
            capabilities: [capability({ state: "certified", truth: "unknown", routable: false })],
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-conj");

    const chip = screen.getByTestId("capability-routable-conj-1-chat");
    expect(chip.textContent ?? "").toMatch(/not routable/i);
    expect(chip.textContent ?? "").not.toMatch(/(^|[^t])\broutable\b(?!\s*$)/i);

    // Both halves of the conjunction stay visible beside it, so the operator can
    // see WHICH half failed.
    const cell = screen.getByTestId("capability-conj-1-chat");
    expect(cell.textContent ?? "").toMatch(/certified/i);
    expect(cell.textContent ?? "").toMatch(/unknown/i);
  });

  it("renders certified + supported as routable", async () => {
    // The other direction, so the test above cannot pass by rendering
    // "not routable" unconditionally.
    mockModels([
      group({
        model_id: "model-ok",
        offerings: [offering({ provider_model_id: "ok-1", capabilities: [capability()] })],
      }),
    ]);
    renderSurface();
    await expandGroup("model-ok");

    const chip = screen.getByTestId("capability-routable-ok-1-chat");
    expect(chip.textContent ?? "").toMatch(/routable/i);
    expect(chip.textContent ?? "").not.toMatch(/not routable/i);
  });

  it("renders every certification state distinctly", async () => {
    const states = [
      "discovered",
      "observed",
      "probing",
      "certified",
      "suspended",
      "expired",
    ] as const;
    mockModels([
      group({
        model_id: "model-states",
        offerings: states.map((state, i) =>
          offering({
            provider_model_id: `state-${i}`,
            capabilities: [capability({ state, truth: "unknown", routable: false })],
          }),
        ),
      }),
    ]);
    renderSurface();
    await expandGroup("model-states");

    const rendered = new Set<string>();
    states.forEach((state, i) => {
      const cell = screen.getByTestId(`capability-state-${i}-chat`);
      const text = (cell.textContent ?? "").toLowerCase();
      expect(text, `state ${state} must name itself`).toContain(state);
      rendered.add(text);
    });
    // Distinct, not six copies of the same chip.
    expect(rendered.size).toBe(states.length);
  });

  it("renders certified + supported + not-yet-routable as 'Not yet effective', never as an inconsistency", async () => {
    // This phase, the server's routable is a THREE-term conjunction (certified
    // AND supported AND effective — 04 §5), and `effective` is hardcoded false
    // for every capability (internal/httpapi/models.go: no transport registry
    // yet). A certified+supported capability therefore ALWAYS comes back
    // routable:false right now. That is not a bug and not a disagreement
    // between the API's fields — it is this phase's honest, expected state, so
    // the badge must say so plainly rather than accusing the API of being
    // inconsistent with itself.
    mockModels([
      group({
        model_id: "model-not-yet-effective",
        offerings: [
          offering({
            provider_model_id: "nye-1",
            capabilities: [capability({ state: "certified", truth: "supported", routable: false })],
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-not-yet-effective");

    const chip = screen.getByTestId("capability-routable-nye-1-chat");
    expect(chip.textContent ?? "").toMatch(/not yet effective/i);
    expect(chip.textContent ?? "").not.toMatch(/not routable/i);
    expect(chip.querySelector("[title]")?.getAttribute("title") ?? "").toMatch(
      /awaiting the transport-effectiveness registry/i,
    );

    // The old "Inconsistent API answer" badge is gone entirely — the server's
    // routable is trusted verbatim, never recomputed or second-guessed here.
    expect(screen.getByTestId("capability-nye-1-chat").textContent ?? "").not.toMatch(
      /inconsistent/i,
    );
  });

  it("renders a plain 'Not routable' (never 'Not yet effective') when the capability is not certified+supported", async () => {
    mockModels([
      group({
        model_id: "model-not-routable",
        offerings: [
          offering({
            provider_model_id: "nr-1",
            capabilities: [capability({ state: "suspended", truth: "unknown", routable: false })],
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-not-routable");

    const chip = screen.getByTestId("capability-routable-nr-1-chat");
    expect(chip.textContent ?? "").toMatch(/not routable/i);
    expect(chip.textContent ?? "").not.toMatch(/not yet effective/i);
    expect(chip.querySelector("[title]")?.getAttribute("title") ?? "").toMatch(
      /not certified as supported yet/i,
    );
  });
});

describe("ModelsSurface — provider identity per row/group (owner requirement 2026-08-05b)", () => {
  // The canonical model_id is derived from a provider-scoped hash
  // (models.CanonicalKey(providerID, providerModelID)), so two providers can
  // never land in the SAME group by construction — but that guarantee is only
  // as good as the VISUAL identity on top of it. This test pins that a
  // display-name collision across two providers renders as two separate,
  // fully-identified rows: never merged, never silently deduplicated.
  it("renders two providers offering the same display name as two separate group rows, each carrying that provider's own logo and name", async () => {
    mockModels([
      group({
        model_id: "model-shared-zen",
        display_name: "Shared Model",
        offerings: [
          offering({
            provider_id: "opencode-zen",
            account_id: "acct-1",
            provider_model_id: "shared-zen-1",
          }),
        ],
      }),
      group({
        model_id: "model-shared-claude",
        display_name: "Shared Model",
        offerings: [
          offering({
            provider_id: "claude-code",
            account_id: "acct-2",
            provider_model_id: "shared-claude-1",
          }),
        ],
      }),
    ]);
    renderSurface();

    const cardA = await screen.findByTestId("model-group-model-shared-zen");
    const cardB = screen.getByTestId("model-group-model-shared-claude");

    // Never merged and never skipped: the same display name shows up on BOTH
    // cards, not collapsed into one.
    expect(within(cardA).getByText("Shared Model")).toBeTruthy();
    expect(within(cardB).getByText("Shared Model")).toBeTruthy();

    // Each row/group is identifiable by ITS OWN provider's logo + name — never
    // the other provider's.
    expect(within(cardA).getByRole("img", { name: "opencode-zen" })).toBeTruthy();
    expect(within(cardB).getByRole("img", { name: "claude-code" })).toBeTruthy();
    expect(within(cardA).queryByRole("img", { name: "claude-code" })).toBeNull();
    expect(within(cardB).queryByRole("img", { name: "opencode-zen" })).toBeNull();
  });
});

describe("ModelsSurface — capabilities as icon chips, never bare words (owner requirement 2026-08-05a)", () => {
  it("renders a capability as the shared icon-chip component, not its bare operation name as text", async () => {
    mockModels([
      group({
        offerings: [
          offering({
            provider_model_id: "vision-1",
            capabilities: [capability({ operation: "vision", provenance: "declared" })],
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-zen-chat");

    const cell = screen.getByTestId("capability-vision-1-vision");
    // The shared CapabilityChips renderer: an accessible icon box labelled by
    // the operation, carrying a tooltip — never a plain visible text node
    // reading the operation name on its own.
    const chip = within(cell).getByRole("img", { name: "vision" });
    expect(chip.className).toMatch(/vnd-capability-icon-box/);
    expect(chip.getAttribute("title") ?? "").toMatch(/VISION/);
    expect(within(cell).queryByText("vision")).toBeNull();
  });
});

describe("ModelsSurface — honest context-provenance markers (owner requirement 2026-08-05c)", () => {
  it("marks a provider-declared (unverified) context with ≈, never ✓", async () => {
    mockModels([
      group({
        model_id: "model-ctx-cap",
        offerings: [
          offering({
            provider_model_id: "ctx-cap-1",
            effective_context_tokens: 200_000,
            context_provenance: "provider_cap",
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-ctx-cap");

    const cell = screen.getByTestId("offering-context-ctx-cap-1");
    expect(cell.textContent ?? "").toMatch(/≈/);
    expect(cell.textContent ?? "").toMatch(/200K/);
    expect(cell.textContent ?? "").not.toMatch(/✓/);

    const mark = cell.querySelector('[title*="declared by provider"]');
    expect(mark).toBeTruthy();
    expect(mark?.getAttribute("title") ?? "").toMatch(/verified by.*context probe/i);
  });

  it("marks a native, probe-verified context with ✓, never ≈", async () => {
    mockModels([
      group({
        model_id: "model-ctx-native",
        offerings: [
          offering({
            provider_model_id: "ctx-native-1",
            effective_context_tokens: 131072,
            context_provenance: "native",
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-ctx-native");

    const cell = screen.getByTestId("offering-context-ctx-native-1");
    expect(cell.textContent ?? "").toMatch(/✓/);
    expect(cell.textContent ?? "").not.toMatch(/≈/);
  });

  it("renders plain 'ctx unknown' with no ≈/✓ mark when context is unknown", async () => {
    mockModels([
      group({
        model_id: "model-ctx-unknown",
        offerings: [
          offering({
            provider_model_id: "ctx-unk-1",
            effective_context_tokens: null,
            context_provenance: "unknown",
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-ctx-unknown");

    const cell = screen.getByTestId("offering-context-ctx-unk-1");
    expect(cell.textContent ?? "").toMatch(/ctx unknown/i);
    expect(cell.textContent ?? "").not.toMatch(/≈|✓/);
  });

  // The group header shows the MAX effective context across offerings
  // (groupContext). Honesty requires the marker to describe the SOURCE
  // offering of that max, not an optimistic blend — so when the larger value
  // is only provider-declared, the header must say ≈ even though a smaller,
  // native-verified offering also exists underneath it.
  it("derives the group header's marker from the source offering of the shown max, not the best provenance available anywhere in the group", async () => {
    mockModels([
      group({
        model_id: "model-ctx-group",
        offerings: [
          offering({
            provider_model_id: "ctx-group-native",
            effective_context_tokens: 100_000,
            context_provenance: "native",
          }),
          offering({
            provider_model_id: "ctx-group-cap",
            account_id: "acct-2",
            effective_context_tokens: 200_000,
            context_provenance: "provider_cap",
          }),
        ],
      }),
    ]);
    renderSurface();

    const header = await screen.findByTestId("model-group-context-model-ctx-group");
    expect(header.textContent ?? "").toMatch(/≈/);
    expect(header.textContent ?? "").toMatch(/200K/);
    expect(header.textContent ?? "").not.toMatch(/✓/);
  });
});

describe("ModelsSurface — unknowns are never fabricated", () => {
  it("renders an unknown effective context as unknown, and never as 0", async () => {
    mockModels([
      group({
        model_id: "model-noctx",
        native_context_tokens: null,
        offerings: [offering({ provider_model_id: "noctx-1", effective_context_tokens: null })],
      }),
    ]);
    renderSurface();
    await expandGroup("model-noctx");

    const cell = screen.getByTestId("offering-context-noctx-1");
    expect(cell.textContent ?? "").toMatch(/unknown/i);
    // The whole point: no zero anywhere in that cell.
    expect(cell.textContent ?? "").not.toMatch(/\b0\b/);
  });

  it("renders an unknown quality rating as unknown, and never as a score of 0", async () => {
    mockModels([
      group({
        model_id: "model-noq",
        quality_rating: null,
        offerings: [
          offering({ provider_model_id: "noq-1", quality_score: 0, quality_known: false }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-noq");

    const cell = screen.getByTestId("offering-quality-noq-1");
    expect(cell.textContent ?? "").toMatch(/unknown|not rated/i);
    expect(cell.textContent ?? "").not.toMatch(/\b0(\.0+)?\b/);
  });

  it("renders catalog_only as never entering a tier, and explicitly NOT as a failure", async () => {
    mockModels([
      group({
        model_id: "model-media",
        display_name: "Image Only",
        offerings: [
          offering({
            provider_model_id: "media-1",
            availability: "catalog_only",
            classification: "media_only",
            capabilities: [
              capability({
                operation: "image",
                state: "certified",
                truth: "supported",
                routable: true,
              }),
            ],
            tiers: {},
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-media");

    const cell = screen.getByTestId("offering-availability-media-1");
    const text = cell.textContent ?? "";
    expect(text).toMatch(/catalog only/i);
    expect(text).toMatch(/never enters a tier|not in any tier/i);
    // 04 §5: "visible, never entering the tiers, NOT counted as a failure". The
    // assertion is on the POSITIVE claim plus the absence of a fault TONE —
    // asserting the string "fail" never appears would forbid the honest wording
    // "not a failure", which is the clearest way to say it.
    expect(text).toMatch(/not a failure|not counted as a failure/i);
    expect(cell.querySelector(".vn-badge--critical")).toBeNull();
    expect(cell.querySelector(".vn-badge--warning")).toBeNull();
  });
});

describe("ModelsSurface — quality rating provenance (Plan 3, local-benchmark-rating)", () => {
  it("labels a group's known rating as coming from the Local benchmark", async () => {
    // 87 is the 0-100 column value; the surface shows ONE scale (0..1) so the
    // header and the offering rows can never read as two different ratings.
    mockModels([group({ model_id: "model-rated", quality_rating: 87 })]);
    renderSurface();

    const cell = await screen.findByTestId("model-group-rating-model-rated");
    expect(cell.textContent ?? "").toMatch(/0\.87/);
    expect(within(cell).getByTitle(/local benchmark/i)).not.toBeNull();
  });

  it("shows the group header and the offering row on the SAME scale", async () => {
    // The whole-branch review's self-contradiction: the header rendered the
    // raw 0-100 column while the row rendered quality_score. One honest
    // backend state must produce one number on both surfaces.
    mockModels([
      group({
        model_id: "model-one-scale",
        quality_rating: 72.5,
        offerings: [
          offering({ provider_model_id: "one-scale-1", quality_score: 0.725, quality_known: true }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-one-scale");

    const header = screen.getByTestId("model-group-rating-model-one-scale");
    const row = screen.getByTestId("offering-quality-one-scale-1");
    expect(header.textContent ?? "").toMatch(/0\.72|0\.73/);
    expect(row.textContent ?? "").toMatch(/0\.72|0\.73/);
    // And neither surface shows the raw column value.
    expect(header.textContent ?? "").not.toMatch(/72\.5/);
    expect(row.textContent ?? "").not.toMatch(/72\.5/);
  });

  it("labels a known offering quality score as coming from the Local benchmark", async () => {
    mockModels([
      group({
        model_id: "model-rated-offering",
        offerings: [
          offering({ provider_model_id: "rated-1", quality_score: 0.87, quality_known: true }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-rated-offering");

    const cell = screen.getByTestId("offering-quality-rated-1");
    expect(cell.textContent ?? "").toMatch(/0\.87/);
    expect(within(cell).getByTitle(/local benchmark/i)).not.toBeNull();
  });

  it("dates the Local benchmark provenance on BOTH rating badges", async () => {
    // Spec line ~205: the provenance reads "local benchmark, <date>". The date
    // is the latest run's finished_at, rendered as a locale-independent ISO
    // day so a reader in any timezone sees the same string the server sent.
    mockModels([
      group({
        model_id: "model-dated",
        quality_rating: 87,
        latest_benchmark: { finished_at: "2026-08-04T09:30:00Z", requests: 3, successes: 3 },
        offerings: [
          offering({ provider_model_id: "dated-1", quality_score: 0.87, quality_known: true }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-dated");

    const header = screen.getByTestId("model-group-rating-model-dated");
    expect(within(header).getByTitle(/local benchmark, 2026-08-04/i)).not.toBeNull();
    const row = screen.getByTestId("offering-quality-dated-1");
    expect(within(row).getByTitle(/local benchmark, 2026-08-04/i)).not.toBeNull();
  });

  it("marks a rating the latest partial run did not refresh", async () => {
    // The stale-rating hole: a partial run withholds the rating and leaves the
    // previous one in place, so the badge must not present the surviving
    // rating as that run's result.
    mockModels([
      group({
        model_id: "model-stale",
        quality_rating: 64,
        latest_benchmark: { finished_at: "2026-08-05T11:00:00Z", requests: 3, successes: 2 },
      }),
    ]);
    renderSurface();

    const header = await screen.findByTestId("model-group-rating-model-stale");
    const title = within(header).getByTitle(/local benchmark/i).getAttribute("title") ?? "";
    expect(title).toMatch(/2026-08-05/);
    expect(title).toMatch(/2 of 3/);
    expect(title).toMatch(/earlier run|withheld|not from/i);
  });

  it("says only 'Local benchmark' when no run has ever been recorded", async () => {
    // A rating with no run to date it must NOT invent one.
    mockModels([
      group({ model_id: "model-undated", quality_rating: 50, latest_benchmark: null }),
    ]);
    renderSurface();

    const header = await screen.findByTestId("model-group-rating-model-undated");
    const title = within(header).getByTitle(/local benchmark/i).getAttribute("title") ?? "";
    expect(title).toBe("Local benchmark");
    expect(title).not.toMatch(/\d{4}-\d{2}-\d{2}/);
  });
});

describe("ModelsSurface — triggers", () => {
  it("sends the CSRF token when triggering discovery", async () => {
    const mock = mockModels([group()], {
      "POST /api/control/v1/accounts/acct-1/discover": () =>
        jsonResponse(202, {
          data: { job_id: "job-disc", status_url: "/api/control/v1/jobs/job-disc" },
        }),
      "GET /api/control/v1/jobs/job-disc": () =>
        jsonResponse(200, { data: { job_id: "job-disc", kind: "discovery", status: "running" } }),
    });
    renderSurface();
    await expandGroup("model-zen-chat");

    fireEvent.click(screen.getByRole("button", { name: /discover/i }));

    await waitFor(() => {
      const call = mock.mock.calls.find(
        ([input, init]) =>
          String(input) === "/api/control/v1/accounts/acct-1/discover" && init?.method === "POST",
      );
      expect(call).toBeTruthy();
    });
    const call = mock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/control/v1/accounts/acct-1/discover" && init?.method === "POST",
    ) as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(call[1].headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
  });

  it("probes the offering-operation id the API reported, with CSRF", async () => {
    // P6-CAPI-EXTRA-2 added `offering_operation_id` to capabilityJSON, which is
    // what POST /offerings/{id}/probe is keyed by. The control uses THAT id
    // verbatim — never one composed from the provider_model_id, which would
    // address a different row.
    const mock = mockModels(
      [
        group({
          offerings: [
            offering({
              capabilities: [capability({ offering_operation_id: "real-op-id-42" })],
            }),
          ],
        }),
      ],
      {
        "POST /api/control/v1/offerings/real-op-id-42/probe": () =>
          jsonResponse(202, {
            data: { job_id: "job-probe", status_url: "/api/control/v1/jobs/job-probe" },
          }),
        "GET /api/control/v1/jobs/job-probe": () =>
          jsonResponse(200, { data: { job_id: "job-probe", kind: "probe", status: "running" } }),
      },
    );
    renderSurface();
    await expandGroup("model-zen-chat");

    const probe = screen.getByTestId("probe-zen-chat-1-chat");
    expect(probe).toHaveProperty("disabled", false);
    fireEvent.click(probe);

    await waitFor(() => {
      const call = mock.mock.calls.find(
        ([input, init]) =>
          String(input) === "/api/control/v1/offerings/real-op-id-42/probe" &&
          init?.method === "POST",
      );
      expect(call).toBeTruthy();
    });
    const call = mock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/control/v1/offerings/real-op-id-42/probe" &&
        init?.method === "POST",
    ) as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(call[1].headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
  });

  it("keeps the probe control disabled when the API reports no offering-operation id", async () => {
    // An operation with no offering_operations row has nothing to probe, and the
    // API omits the field to say so. Composing an id from the provider_model_id
    // would address the wrong row or 404 — so the control stays disabled with the
    // reason stated, exactly as before this field existed.
    const mock = mockModels([
      group({
        offerings: [
          offering({
            capabilities: [capability({ offering_operation_id: undefined })],
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-zen-chat");

    const probe = screen.getByTestId("probe-zen-chat-1-chat");
    expect(probe).toHaveProperty("disabled", true);
    expect(probe.getAttribute("title") ?? "").toMatch(/offering-operation id/i);

    fireEvent.click(probe);
    await waitFor(() => {
      const probeCall = mock.mock.calls.find(([input]) => String(input).includes("/probe"));
      expect(probeCall).toBeUndefined();
    });
  });

  it("reports an accepted trigger as a job in flight, never as instant success", async () => {
    mockModels([group()], {
      "POST /api/control/v1/accounts/acct-1/discover": () =>
        jsonResponse(202, {
          data: { job_id: "job-disc", status_url: "/api/control/v1/jobs/job-disc" },
        }),
      "GET /api/control/v1/jobs/job-disc": () =>
        jsonResponse(200, { data: { job_id: "job-disc", kind: "discovery", status: "running" } }),
    });
    renderSurface();
    await expandGroup("model-zen-chat");

    fireEvent.click(screen.getByRole("button", { name: /discover/i }));

    const outcome = await screen.findByTestId("job-outcome");
    expect(outcome.textContent ?? "").toMatch(/running|started|in progress/i);
    expect(outcome.textContent ?? "").not.toMatch(/\bcompleted\b|\bsucceeded\b|\bdone\b/i);
  });

  it("states the honest completion outcome instead of the stale 'no canonical quality source' claim", async () => {
    // Task 5 wired a REAL local benchmark: a completed job now sometimes DOES
    // write models.quality_rating (every request in the suite succeeded) and
    // sometimes withholds it (a partial failure). GET /jobs/{id}'s
    // result_ref is the same static "/api/control/v1/models" reference
    // either way, so this surface cannot tell which outcome happened from the
    // job read alone — it must say so honestly rather than repeat the old,
    // now-false claim that no canonical quality source exists at all.
    mockModels(
      [
        group({
          quality_rating: null,
          offerings: [offering({ quality_known: false, quality_score: 0 })],
        }),
      ],
      {
        "POST /api/control/v1/models/model-zen-chat/benchmark": () =>
          jsonResponse(202, {
            data: { job_id: "job-bench", status_url: "/api/control/v1/jobs/job-bench" },
          }),
        "GET /api/control/v1/jobs/job-bench": () =>
          jsonResponse(200, {
            data: { job_id: "job-bench", kind: "benchmark", status: "completed" },
          }),
      },
    );
    renderSurface();

    fireEvent.click(await screen.findByRole("button", { name: /benchmark/i }));

    const outcome = await screen.findByTestId("job-outcome");
    await waitFor(() => expect(outcome.textContent ?? "").toMatch(/completed/i));
    // The stale copy is gone.
    expect(outcome.textContent ?? "").not.toMatch(/no canonical quality source/i);
    // The honest replacement names the real mechanism and provenance, and
    // states the condition rather than asserting an unverifiable specific
    // outcome.
    expect(outcome.textContent ?? "").toMatch(/local benchmark/i);
    expect(outcome.textContent ?? "").toMatch(/withheld|unchanged/i);
  });

  it("explains a benchmark 409 as enrichment being disabled, not a permission problem", async () => {
    mockModels([group()], {
      "POST /api/control/v1/models/model-zen-chat/benchmark": () =>
        jsonResponse(409, {
          error: {
            code: "enrichment_disabled",
            message: "enrichment is disabled",
            request_id: "req-1",
            retryable: false,
          },
        }),
    });
    renderSurface();

    fireEvent.click(await screen.findByRole("button", { name: /benchmark/i }));

    const outcome = await screen.findByTestId("job-outcome");
    await waitFor(() => expect(outcome.textContent ?? "").toMatch(/enrichment/i));
    // The positive claims: a STATE conflict the owner can resolve, and where.
    expect(outcome.textContent ?? "").toMatch(/disabled/i);
    expect(outcome.textContent ?? "").toMatch(/settings/i);
    expect(outcome.textContent ?? "").toMatch(/not a permission problem/i);
    // And never framed as an authorization failure.
    expect(outcome.textContent ?? "").not.toMatch(/forbidden|unauthori|sign in|log in/i);
  });
});

describe("ModelsSurface — pagination honesty", () => {
  it("follows the cursor rather than presenting one page as the whole catalog", async () => {
    const mock = createFetchMock({
      [MODELS_URL]: () =>
        jsonResponse(200, {
          data: [group({ model_id: "page-1", display_name: "Page One Model" })],
          meta: { next_cursor: "c2" },
        }),
      "GET /api/control/v1/models?cursor=c2&limit=200": () =>
        jsonResponse(200, {
          data: [group({ model_id: "page-2", display_name: "Page Two Model" })],
        }),
      [CENSUS_URL]: () => jsonResponse(200, censusBody()),
    });
    vi.stubGlobal("fetch", mock);
    renderSurface();

    await screen.findByText("Page One Model");
    await screen.findByText("Page Two Model");
  });
});

describe("ModelsSurface — loading, empty, error", () => {
  it("renders a loading state before the catalog arrives", () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderSurface();
    expect(screen.getByRole("status").getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders a truthful live-model empty state when no healthy account exposes models", async () => {
    mockModels([]);
    const { container } = renderSurface();
    await screen.findByText("No live models");
    expect(container.textContent ?? "").toMatch(/healthy connected provider account/i);
    expect(container.textContent ?? "").not.toMatch(/no models discovered/i);
  });

  it("renders an error state instead of an empty catalog when the API fails", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [MODELS_URL]: () =>
          jsonResponse(500, {
            error: {
              code: "internal",
              message: "internal error",
              request_id: "r1",
              retryable: true,
            },
          }),
        [CENSUS_URL]: () => jsonResponse(200, censusBody()),
      }),
    );
    const { container } = renderSurface();

    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load/i));
    // An error must never read as "you have no live models".
    expect(container.textContent ?? "").not.toMatch(/no live models/i);
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [MODELS_URL]: () =>
          jsonResponse(401, {
            error: {
              code: "session_expired",
              message: "session expired",
              request_id: "r2",
              retryable: false,
            },
          }),
        [CENSUS_URL]: () => jsonResponse(200, censusBody()),
      }),
    );
    render(<ModelsSurface csrfToken={CSRF_TOKEN} onSessionExpired={onSessionExpired} />);
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });
});

describe("ModelsSurface — secrets and accessibility", () => {
  it("renders no account external id or credential material", async () => {
    mockModels([group()]);
    const { container } = renderSurface();
    await expandGroup("model-zen-chat");

    // account_id is a correlation id and is legitimately shown; there is no
    // external_id or credential field in this projection at all, and nothing
    // credential-shaped may appear.
    expect(container.innerHTML).not.toMatch(/vk_live_|sk-|Bearer /);
  });

  it("has no axe violations with the catalog expanded", async () => {
    mockModels([
      group(),
      group({
        model_id: "model-2",
        display_name: "Second Model",
        offerings: [
          offering({
            provider_model_id: "second-1",
            effective_context_tokens: null,
            quality_known: false,
            capabilities: [capability({ state: "observed", truth: "unknown", routable: false })],
          }),
        ],
      }),
    ]);
    const { container } = renderSurface();
    await expandGroup("model-zen-chat");
    await expandGroup("model-2");
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when empty", async () => {
    mockModels([]);
    const { container } = renderSurface();
    await screen.findByText("No live models");
    await assertNoAxeViolations(container);
  });
});

describe("ModelsSurface — review-queue banner integration (P6-UI-012)", () => {
  it("renders the banner and filters the catalog to the backlog when asked", async () => {
    mockModels(
      [
        group({
          model_id: "model-clean",
          display_name: "Clean Model",
          offerings: [offering({ provider_model_id: "clean-1", capabilities: [capability()] })],
        }),
        group({
          model_id: "model-backlog",
          display_name: "Backlog Model",
          offerings: [
            offering({
              provider_model_id: "backlog-1",
              capabilities: [capability({ state: "observed", truth: "unknown", routable: false })],
            }),
          ],
        }),
      ],
      {
        [CENSUS_URL]: () =>
          jsonResponse(200, censusBody([{ reason: "capability_not_certified", count: 1 }])),
      },
    );
    renderSurface();

    // Both groups before filtering.
    await screen.findByText("Clean Model");
    screen.getByText("Backlog Model");

    fireEvent.click(await screen.findByRole("button", { name: /review the backlog/i }));

    await waitFor(() => expect(screen.queryByText("Clean Model")).toBeNull());
    screen.getByText("Backlog Model");
  });

  it("does NOT flag a fully certified+supported model as needing review, even when routable is false", async () => {
    // The real /models read model hardcodes transport/native effectiveness as
    // UNKNOWN this phase, so `routable` comes back false for EVERY capability.
    // "Needs review" must key off certification (what the banner counts), not
    // routability — otherwise every model reads "Needs review" forever.
    mockModels([
      group({
        model_id: "model-certified",
        display_name: "Certified Model",
        offerings: [
          offering({
            provider_model_id: "cert-1",
            capabilities: [
              capability({
                operation: "chat",
                state: "certified",
                truth: "supported",
                routable: false,
              }),
              capability({
                operation: "tools",
                state: "certified",
                truth: "supported",
                routable: false,
              }),
            ],
          }),
        ],
      }),
    ]);
    renderSurface();

    await screen.findByText("Certified Model");
    expect(screen.queryByText(/needs review/i)).toBeNull();
  });

  it("shows the review banner's count grouped by reason", async () => {
    mockModels([group()], {
      [CENSUS_URL]: () =>
        jsonResponse(200, censusBody([{ reason: "capability_not_certified", count: 4 }])),
    });
    renderSurface();

    const banner = await screen.findByTestId("review-queue-banner");
    expect(within(banner).getByText("capability_not_certified")).toBeTruthy();
    expect(banner.textContent ?? "").toMatch(/4/);
  });
});

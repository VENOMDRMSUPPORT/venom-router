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

function group(overrides: Partial<ModelGroup> = {}): ModelGroup {
  return {
    model_id: "model-zen-chat",
    display_name: "Zen Chat",
    native_context_tokens: 262144,
    quality_rating: 0.82,
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

  it("fails closed when the API's routable flag contradicts the state/truth it reported", async () => {
    // A server that said routable:true for a non-conjunction pair is a bug
    // somewhere. The surface must not amplify it into a routable claim.
    mockModels([
      group({
        model_id: "model-contradiction",
        offerings: [
          offering({
            provider_model_id: "contra-1",
            capabilities: [capability({ state: "suspended", truth: "unknown", routable: true })],
          }),
        ],
      }),
    ]);
    renderSurface();
    await expandGroup("model-contradiction");

    const chip = screen.getByTestId("capability-routable-contra-1-chat");
    expect(chip.textContent ?? "").toMatch(/not routable/i);
    expect(screen.getByTestId("capability-contra-1-chat").textContent ?? "").toMatch(
      /inconsistent/i,
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

  it("never claims a rating was updated when a benchmark completes without one", async () => {
    // QualityIndex is nil in production, so this is the NORMAL outcome today,
    // not an edge case: the job completes and no rating is written.
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
    // A completed benchmark job is NOT a new rating.
    expect(outcome.textContent ?? "").not.toMatch(/rating updated|rated|score updated/i);
    expect(outcome.textContent ?? "").toMatch(/no rating|without a rating|rating unchanged/i);
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

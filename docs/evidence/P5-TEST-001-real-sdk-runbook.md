# P5-TEST-001 — Real-SDK evidence runbook

This is a **manual, dated evidence procedure** for the one part of the P5 phase
gate that cannot run automatically in CI: pointing an **actual OpenAI SDK** at a
running Venom and exercising chat + streaming + tools + vision.

## Why this is a runbook and not a CI test

The P5-TEST-001 card is explicit: *"Uses fake provider backends for CI
determinism; a real-provider run is recorded evidence."* Two hard constraints
shape this, exactly as they did for P2b-TEST-003:

1. **No new dependency may be added** — there is no `openai` package in `go.mod`
   or any `package.json`, and adding one is forbidden. A CI test therefore cannot
   `import` a real SDK.
2. **CI is credential-free** — no real provider key is ever stored in CI.

So the gate is split:

- **The CI gate = wire-protocol conformance** (fully automated, runs every CI
  build): `internal/httpapi/p5gate_sdk_test.go`, the `TestP5Gate_*` suite. It
  drives `POST /v1/chat/completions` and `/v1/models` with a plain `net/http`
  client — the same bytes an SDK sends — and asserts the **exact** shapes any
  OpenAI SDK parses:
  - the completion object (`object: "chat.completion"`, `id`, `model`,
    `choices[].index`, `choices[].message.role/content`, `choices[].finish_reason`),
  - the streaming chunk (`object: "chat.completion.chunk"`,
    `choices[].delta.content`, `data: ` framing, `[DONE]` terminator),
  - the `Content-Type`s (`application/json`, `text/event-stream`),
  - the tools array reaching the provider and `tool_calls` returning,
  - a vision image part reaching the provider in the OpenAI array-content form,
  - all three tier model names routing (Lite free-only),
  - the `venom` extension clamp reported in `X-Venom-*`, `required_capabilities`
    as hard gates, extension survival through streaming, and the typed
    `venom_invalid_extension` rejection,
  - usage + route-decision rows recorded, and the content canary.

  **Because an SDK observes nothing beyond these wire shapes, the CI gate already
  proves an SDK cannot break against Venom's protocol.** This runbook adds the
  final empirical confirmation against a *real* SDK and a *real* provider.

- **The real-SDK run = this procedure**, plus the opt-in harness
  `internal/httpapi/p5gate_realsdk_test.go` (`TestP5Gate_RealSDK_OptIn`), which
  `t.Skip`s unless `VENOM_E2E_REAL_SDK_BASE_URL` and `VENOM_E2E_REAL_SDK_KEY` are
  both set. `TestP5Gate_RealSDKHarnessSkipsWithoutEnv` proves it is inert in CI.

Only run the steps below against a provider account you are willing to use.

## Prerequisites

1. A built Venom binary or `go run ./cmd/venom`, started normally and reachable
   at its loopback control-plane URL (see
   [01-architecture §6a/§6b](../01-architecture.md)); the default data-plane base
   is `http://127.0.0.1:8081/v1`.
2. At least one **connected, discovered, and certified** provider account with a
   real credential (e.g. a free opencode-zen key enrolled via the dashboard),
   so the fleet actually has a routable offering for the tier you test. Vision
   requires a vision-certified offering.
3. Python `openai` (`pip install openai`) and/or Node `openai`
   (`npm i openai`) — installed in YOUR environment, never added to this repo.

## Steps

1. **Start Venom** and complete first-run owner setup / login in the dashboard.
2. **Mint a Venom API key**: in the dashboard's key-management view create a key
   (or `POST /api/control/v1/keys`), and copy the one-time `vk_live_...` secret.
3. **Point the SDK at Venom.** Python:
   ```python
   from openai import OpenAI
   c = OpenAI(base_url="http://127.0.0.1:8081/v1", api_key="vk_live_...")
   print(c.chat.completions.create(
       model="venom/pro",
       messages=[{"role": "user", "content": "Say hi in five words."}]))
   ```
   Node:
   ```js
   import OpenAI from "openai";
   const c = new OpenAI({ baseURL: "http://127.0.0.1:8081/v1", apiKey: "vk_live_..." });
   console.log(await c.chat.completions.create({
     model: "venom/pro",
     messages: [{ role: "user", content: "Say hi in five words." }],
   }));
   ```
4. **Streaming**: repeat step 3 with `stream=True` (Python) / `stream: true`
   (Node) and confirm the SDK yields incremental `delta.content` chunks and
   terminates cleanly (the SDK stops at `[DONE]`).
5. **Tools**: send a request with a `tools` function definition and a prompt
   that elicits a tool call; confirm the SDK surfaces `tool_calls`.
6. **Vision**: send a message whose content is the array form with an
   `image_url` (a `data:image/...;base64,...` URL) against a vision-certified
   offering; confirm a normal completion returns.
7. **Extension**: send `extra_body={"venom": {"thinking_budget": "ultra"}}`
   (Python) on `venom/pro` and inspect the response headers for
   `X-Venom-Thinking-Clamped: true`. Send an invalid `venom.thinking_budget`
   and confirm the SDK raises a 400 whose body code is `venom_invalid_extension`.

## Evidence to paste back (dated)

Record, with the date and the Venom commit (`git rev-parse HEAD`):

- The SDK version(s) used (`pip show openai` / `npm ls openai`).
- The chat completion object the SDK printed (redact the content if sensitive) —
  confirming `object == "chat.completion"`, a populated `choices[0].message`,
  and a `finish_reason`.
- Confirmation streaming yielded multiple chunks and ended cleanly.
- Confirmation `tool_calls` surfaced.
- Confirmation the vision request returned a completion.
- The observed `X-Venom-Thinking-Clamped` header value and the
  `venom_invalid_extension` error body for the invalid case.
- A note of any provider-specific behavior observed (this is empirical evidence,
  not a Venom contract).

## Known limits (do not record as failures)

- **Applied thinking above `none`** is not reachable yet: the candidate snapshot
  does not populate reasoning certification, so the clamp is reported but the
  *applied* level is `none` regardless of request. This is the same limit the
  automated gate documents; it is a snapshot-enrichment follow-up, not a P5 gate
  failure.
- **Provider vision quality / correctness** is out of scope here — the gate
  proves the image part is transmitted in the right wire form, not that a given
  provider answers it well (that is the probe/certification side).

## Record

| Date | Venom commit | SDK(s) | Result | Observer |
|------|--------------|--------|--------|----------|
|      |              |        |        |          |

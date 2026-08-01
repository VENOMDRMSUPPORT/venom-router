import { useCallback, useEffect, useRef, useState } from "react";
import {
  Badge,
  Button,
  Card,
  CodeBlock,
  ErrorState,
  FormField,
  KeyValueList,
  SegmentedControl,
  Select,
  Textarea,
} from "@venom/design-system/primitives";
import { TierBadge, type Tier as DSTier } from "@venom/design-system/domain";
import { TIER_MODEL_IDS } from "../connect/generators";

/**
 * The Playground needs NEITHER a CSRF token nor a session-expiry hook, and says so
 * by taking no props at all.
 *
 * /v1 is vk-authenticated, not owner-session authenticated: CSRF protects
 * owner-session mutations and this is not one, so a token here would be meaningless
 * ceremony. And a 401 from /v1 means the pasted VENOM KEY was rejected — it is not
 * an owner-session expiry, so routing it to onSessionExpired would sign the owner
 * out of the console over a mistyped key.
 */
export type PlaygroundSurfaceProps = Record<string, never>;

/** The data-plane endpoint. The public /v1 surface is vk-authenticated, never
 * owner-session authenticated, so this call carries a Bearer key and NO CSRF
 * token — CSRF protects owner-session mutations, and /v1 is not one. */
const CHAT_URL = "/v1/chat/completions";

/** The thinking levels a client may request via the `venom` extension (05 §1a).
 * A request may ask for a level at or below its tier's ceiling; above it is
 * clamped, and the clamp is reported in the X-Venom-* headers. */
const THINKING_LEVELS = ["none", "standard", "extended", "ultra"] as const;

/** How a send ended. `broken` is the one that matters: a stream that died
 * mid-flight left a PARTIAL answer, and presenting that as a finished response
 * would be the surface's worst possible lie. */
type StreamOutcome =
  | { kind: "idle" }
  | { kind: "streaming" }
  | { kind: "done" }
  | { kind: "broken"; reason: string }
  | { kind: "refused"; reason: string }
  | { kind: "error"; code: string; message: string };

/** The `X-Venom-*` route-outcome headers (01 §6c). Collected verbatim; this
 * surface never derives one. */
function collectVenomHeaders(headers: Headers): { name: string; value: string }[] {
  const out: { name: string; value: string }[] = [];
  headers.forEach((value, name) => {
    if (name.toLowerCase().startsWith("x-venom-")) out.push({ name, value });
  });
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

/**
 * The Playground surface (P6-UI-004, 07 §6, 01 §6c).
 *
 * ─── KEY HANDLING IS THE CONSTRAINT THAT SHAPES THIS WHOLE UNIT ─────────────
 *
 * A raw `vk_live_*` key is shown exactly ONCE at creation (09 §3.11) and the server
 * stores only a hash, so this surface CANNOT retrieve an existing key's secret. The
 * owner therefore pastes one. It then lives in `apiKey` state — in memory, for this
 * session, and nowhere else:
 *
 *   - never localStorage or sessionStorage,
 *   - never a URL or query string,
 *   - never a log line,
 *   - never a generated file,
 *   - never rendered outside its own masked input,
 *
 * and it is cleared on unmount. A request without a key is REFUSED locally with the
 * reason stated, rather than fired unauthenticated to produce a 401 that teaches the
 * owner nothing.
 *
 * ─── AND A STREAM THAT BREAKS SAYS SO ───────────────────────────────────────
 *
 * The response is rendered progressively as chunks arrive. If the stream fails
 * before its `[DONE]` terminator, the partial text is KEPT (it is real — the model
 * did emit it) but labelled as an incomplete answer. Silently dropping the
 * terminator and presenting a truncated response as complete is the failure this
 * surface is written to avoid.
 */
export default function PlaygroundSurface() {
  const [apiKey, setApiKey] = useState("");
  const [tier, setTier] = useState<string>(TIER_MODEL_IDS[1]);
  const [thinking, setThinking] = useState<string>("extended");
  const [prompt, setPrompt] = useState("Say hello in five words.");
  const [outcome, setOutcome] = useState<StreamOutcome>({ kind: "idle" });
  const [answer, setAnswer] = useState("");
  const [venomHeaders, setVenomHeaders] = useState<{ name: string; value: string }[]>([]);
  const [requestPreview, setRequestPreview] = useState<string>("");
  const abortRef = useRef<AbortController | null>(null);

  // Clear the key (and abort any in-flight stream) on unmount, so neither
  // outlives the page.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      setApiKey("");
    };
  }, []);

  /** The request body, including the `venom` extension (05 §1b). */
  const buildBody = useCallback(
    () => ({
      model: tier,
      stream: true,
      messages: [{ role: "user", content: prompt }],
      venom: { thinking_budget: thinking },
    }),
    [tier, thinking, prompt],
  );

  const handleSend = useCallback(async () => {
    if (apiKey.trim() === "") {
      // Refused locally. Firing an unauthenticated request would return a 401 that
      // tells the owner nothing they did not already know.
      setOutcome({
        kind: "refused",
        reason:
          "No API key. The playground sends a real request to the public /v1 surface, which authenticates with a Venom key — paste one above. A key is shown only once, at creation, so it cannot be read back from the server.",
      });
      return;
    }

    const body = buildBody();
    // The PREVIEW deliberately shows the body only. The Authorization header is
    // never rendered, so the key cannot reach the DOM through it.
    setRequestPreview(JSON.stringify(body, null, 2));
    setAnswer("");
    setVenomHeaders([]);
    setOutcome({ kind: "streaming" });

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const response = await fetch(CHAT_URL, {
        method: "POST",
        // No CSRF token: /v1 is vk-authenticated, not owner-session authenticated.
        headers: {
          Authorization: `Bearer ${apiKey.trim()}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(body),
        signal: controller.signal,
      });

      // The route outcome is reported in headers on the response itself, so it is
      // available even when the body then fails.
      setVenomHeaders(collectVenomHeaders(response.headers));

      if (!response.ok) {
        let code = "http_error";
        let message = `The router answered ${response.status}.`;
        try {
          const envelope = (await response.json()) as { error?: { code?: string; message?: string } };
          code = envelope.error?.code ?? code;
          message = envelope.error?.message ?? message;
        } catch {
          // A non-JSON error body is left as the status line — never echoed raw,
          // since it could carry provider text.
        }
        setOutcome({ kind: "error", code, message });
        return;
      }

      if (!response.body) {
        setOutcome({ kind: "broken", reason: "The router returned no response body to stream." });
        return;
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let sawTerminator = false;

      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });

        // SSE frames are newline-delimited `data: ` lines.
        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";
        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed.startsWith("data:")) continue;
          const payload = trimmed.slice("data:".length).trim();
          if (payload === "[DONE]") {
            sawTerminator = true;
            continue;
          }
          try {
            const chunk = JSON.parse(payload) as {
              choices?: { delta?: { content?: string } }[];
            };
            const delta = chunk.choices?.[0]?.delta?.content;
            if (typeof delta === "string") setAnswer((prev) => prev + delta);
          } catch {
            // A malformed frame is a broken stream, not a silent skip: dropping it
            // would lose content and still look complete.
            setOutcome({ kind: "broken", reason: "A stream frame could not be parsed." });
            return;
          }
        }
      }

      // The terminator is the ONLY thing that makes an answer complete.
      setOutcome(
        sawTerminator
          ? { kind: "done" }
          : {
              kind: "broken",
              reason:
                "The stream ended without its [DONE] terminator, so the text above is a partial answer — not the model's complete response.",
            },
      );
    } catch (err) {
      if (controller.signal.aborted) return;
      setOutcome({
        kind: "broken",
        reason: `The stream failed before completing (${err instanceof Error ? err.name : "network error"}), so the text above is a partial answer.`,
      });
    } finally {
      abortRef.current = null;
    }
  }, [apiKey, buildBody]);

  const streaming = outcome.kind === "streaming";

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-col gap-3">
          <h2 className="vn-h3">Compose a request</h2>

          <div className="flex flex-col gap-2">
            <label className="vn-caption" htmlFor="playground-key">
              Venom API key
            </label>
            <input
              id="playground-key"
              className="vn-input"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              data-testid="playground-key"
            />
            <span className="vn-caption">
              Held in memory for this session only — never stored, never put in a URL, and cleared
              when you leave this page. A key is shown once at creation and cannot be read back.
            </span>
          </div>

          <SegmentedControl
            label="Tier"
            value={tier}
            options={TIER_MODEL_IDS.map((id) => ({ value: id, label: id }))}
            onChange={setTier}
          />

          <FormField label="Thinking budget (the venom extension)">
            <Select value={thinking} onChange={(e) => setThinking(e.target.value)}>
              {THINKING_LEVELS.map((level) => (
                <option key={level} value={level}>
                  {level}
                </option>
              ))}
            </Select>
          </FormField>

          <FormField label="Prompt">
            <Textarea rows={4} value={prompt} onChange={(e) => setPrompt(e.target.value)} />
          </FormField>

          <div>
            <Button variant="primary" onClick={() => void handleSend()} disabled={streaming}>
              {streaming ? "Streaming…" : "Send"}
            </Button>
          </div>
        </div>
      </Card>

      {outcome.kind === "refused" ? (
        <div data-testid="playground-refusal">
          <ErrorState
            variant="inline"
            code="no_api_key"
            title="Nothing was sent"
            description={outcome.reason}
          />
        </div>
      ) : null}

      {requestPreview === "" ? null : (
        <Card data-testid="playground-request">
          <div className="flex flex-col gap-2">
            <span className="vn-caption">
              Request body — the Authorization header is deliberately not shown.
            </span>
            <CodeBlock label="request" code={requestPreview} />
          </div>
        </Card>
      )}

      {outcome.kind === "idle" ? null : (
        <Card data-testid="playground-response">
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="vn-title-sub">Response</span>
              <TierBadge tier={tier.replace("venom/", "") as DSTier} />
              {outcome.kind === "streaming" ? (
                <Badge tone="info" icon="loader">
                  Streaming
                </Badge>
              ) : null}
              {outcome.kind === "done" ? (
                <Badge tone="healthy" icon="circle-check">
                  Complete
                </Badge>
              ) : null}
              {outcome.kind === "broken" ? (
                <Badge tone="warning" icon="triangle-alert">
                  Incomplete — the stream broke
                </Badge>
              ) : null}
            </div>

            {answer === "" && outcome.kind === "streaming" ? (
              <span className="vn-caption">Waiting for the first chunk…</span>
            ) : (
              <div className="vn-body" data-testid="playground-answer">
                {answer}
              </div>
            )}

            {outcome.kind === "broken" ? (
              <div data-testid="playground-broken">
                <ErrorState
                  variant="inline"
                  code="stream_incomplete"
                  title="This answer is incomplete"
                  description={outcome.reason}
                />
              </div>
            ) : null}

            {outcome.kind === "error" ? (
              <div data-testid="playground-error">
                <ErrorState
                  variant="inline"
                  code={outcome.code}
                  title="The router refused the request"
                  description={outcome.message}
                />
              </div>
            ) : null}

            <div className="flex flex-col gap-1" data-testid="playground-venom-headers">
              <span className="vn-caption">Route outcome (X-Venom-* headers)</span>
              {venomHeaders.length === 0 ? (
                <span className="vn-caption">
                  The router reported no X-Venom-* headers on this response.
                </span>
              ) : (
                <KeyValueList
                  items={venomHeaders.map((h) => ({ key: h.name, value: h.value, mono: true }))}
                />
              )}
            </div>
          </div>
        </Card>
      )}
    </div>
  );
}

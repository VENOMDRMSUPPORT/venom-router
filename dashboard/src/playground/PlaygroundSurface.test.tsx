import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import PlaygroundSurface from "./PlaygroundSurface";

const REAL_KEY = "vk_live_9f3c1d77aa2b4e6c8051d3f4b7e2a9c6";

/** Builds an SSE body from raw frame strings. */
function sseStream(frames: string[], { terminate = true }: { terminate?: boolean } = {}): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  const lines = [...frames];
  if (terminate) lines.push("data: [DONE]");
  return new ReadableStream({
    start(controller) {
      for (const line of lines) controller.enqueue(encoder.encode(line + "\n"));
      controller.close();
    },
  });
}

/** A content delta frame. */
function delta(text: string): string {
  return `data: ${JSON.stringify({ choices: [{ delta: { content: text } }] })}`;
}

/** A streaming 200 with the X-Venom-* route-outcome headers. */
function streamingResponse(
  body: ReadableStream<Uint8Array>,
  headers: Record<string, string> = {
    "X-Venom-Tier": "pro",
    "X-Venom-Provider": "opencode-zen",
    "X-Venom-Thinking-Applied": "extended",
  },
): Response {
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream", ...headers } });
}

/** A stream that delivers one chunk and THEN dies, before any terminator.
 *
 * The error is raised on the SECOND pull rather than inside start(): a stream that
 * enqueues and errors synchronously rejects the very first read(), so the consumer
 * never sees the chunk and the "partial answer is kept" half of the assertion
 * would be vacuous. This shape reproduces the real failure — bytes arrived, then
 * the connection dropped. */
function brokenStream(): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  let delivered = false;
  return new ReadableStream({
    pull(controller) {
      if (!delivered) {
        delivered = true;
        controller.enqueue(encoder.encode(delta("partial ") + "\n"));
        return;
      }
      controller.error(new Error("connection reset"));
    },
  });
}

function renderSurface() {
  return render(<PlaygroundSurface />);
}

function enterKey(key = REAL_KEY): void {
  fireEvent.change(screen.getByTestId("playground-key"), { target: { value: key } });
}

function send(): void {
  fireEvent.click(screen.getByRole("button", { name: /^send$/i }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
  sessionStorage.clear();
});

describe("PlaygroundSurface — streaming", () => {
  it("renders chunks progressively and marks the answer complete on [DONE]", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("Hello "), delta("world")]))));
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(screen.getByTestId("playground-answer").textContent).toBe("Hello world"));
    await waitFor(() => expect(screen.getByTestId("playground-response").textContent ?? "").toMatch(/complete/i));
    expect(screen.queryByTestId("playground-broken")).toBeNull();
  });

  it("sends the venom extension and the chosen tier in the request body", async () => {
    const fetchMock = vi.fn(async () => streamingResponse(sseStream([delta("ok")])));
    vi.stubGlobal("fetch", fetchMock);
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const [, init] = fetchMock.mock.calls[0] as unknown as [RequestInfo | URL, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.model).toBe("venom/pro");
    expect(body.stream).toBe(true);
    // The `venom` extension (05 §1b) is surfaced, not silently dropped.
    expect(body.venom).toEqual({ thinking_budget: "extended" });
  });

  it("authenticates with a Bearer key and NO CSRF token", async () => {
    // /v1 is vk-authenticated, never owner-session authenticated — a CSRF token
    // here would be meaningless ceremony.
    const fetchMock = vi.fn(async () => streamingResponse(sseStream([delta("ok")])));
    vi.stubGlobal("fetch", fetchMock);
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const [, init] = fetchMock.mock.calls[0] as unknown as [
      RequestInfo | URL,
      RequestInit & { headers: Record<string, string> },
    ];
    expect(init.headers.Authorization).toBe(`Bearer ${REAL_KEY}`);
    expect(init.headers["X-CSRF-Token"]).toBeUndefined();
  });

  it("displays the X-Venom-* route outcome headers", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("ok")]))));
    renderSurface();
    enterKey();
    send();

    const headers = await screen.findByTestId("playground-venom-headers");
    await waitFor(() => expect(headers.textContent ?? "").toMatch(/x-venom-tier/i));
    expect(headers.textContent ?? "").toContain("opencode-zen");
    expect(headers.textContent ?? "").toMatch(/extended/);
  });

  it("says so when the router reported no X-Venom-* headers", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("ok")]), {})));
    renderSurface();
    enterKey();
    send();

    const headers = await screen.findByTestId("playground-venom-headers");
    await waitFor(() => expect(headers.textContent ?? "").toMatch(/no x-venom-\* headers/i));
  });
});

describe("PlaygroundSurface — a broken stream is stated, never presented as complete", () => {
  // THE assertion this unit exists for. A stream that dies mid-flight left a
  // PARTIAL answer; rendering it as finished would show the operator a truncated
  // response as though the model had stopped there.
  it("labels a mid-stream failure as incomplete and keeps the partial text", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(brokenStream())));
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(screen.getByTestId("playground-broken")).toBeTruthy());
    const response = screen.getByTestId("playground-response");
    expect(response.textContent ?? "").toMatch(/incomplete/i);
    // And the "Complete" badge — which only a terminated stream earns — is absent.
    // ("Incomplete" contains the substring, so the assertion is on the badge.)
    expect(response.querySelector(".vn-badge--healthy")).toBeNull();
    // The partial text is REAL and is kept — the model did emit it.
    expect(screen.getByTestId("playground-answer").textContent).toContain("partial");
  });

  it("treats a stream that ends without [DONE] as incomplete", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => streamingResponse(sseStream([delta("half an answer")], { terminate: false }))),
    );
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(screen.getByTestId("playground-broken").textContent ?? "").toMatch(/\[DONE\]/));
    expect(screen.getByTestId("playground-response").textContent ?? "").toMatch(/incomplete/i);
  });

  it("treats an unparsable frame as a broken stream rather than skipping it", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => streamingResponse(sseStream([delta("good "), "data: {not json"]))),
    );
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(screen.getByTestId("playground-broken")).toBeTruthy());
  });
});

describe("PlaygroundSurface — no key, no request", () => {
  it("refuses to send without a key, and explains why", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderSurface();

    send();

    const refusal = await screen.findByTestId("playground-refusal");
    expect(refusal.textContent ?? "").toMatch(/no api key/i);
    expect(refusal.textContent ?? "").toMatch(/shown only once|cannot be read back/i);
    // Nothing was fired — an unauthenticated 401 would teach the owner nothing.
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("sends once a key is provided", async () => {
    // The other direction, so the refusal test cannot pass by never sending.
    const fetchMock = vi.fn(async () => streamingResponse(sseStream([delta("ok")])));
    vi.stubGlobal("fetch", fetchMock);
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  });
});

describe("PlaygroundSurface — typed errors", () => {
  it("renders the router's typed error rather than a generic failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: { code: "venom_capability_unsupported", message: "no certified offering serves vision" },
            }),
            { status: 422, headers: { "Content-Type": "application/json" } },
          ),
      ),
    );
    renderSurface();
    enterKey();
    send();

    const error = await screen.findByTestId("playground-error");
    expect(error.textContent ?? "").toContain("venom_capability_unsupported");
    expect(error.textContent ?? "").toMatch(/no certified offering/i);
  });

  it("does not echo a non-JSON error body, which could carry provider text", async () => {
    const rawProviderText = "ZZQQ-upstream-raw-provider-error";
    vi.stubGlobal("fetch", vi.fn(async () => new Response(rawProviderText, { status: 502 })));
    const { container } = renderSurface();
    enterKey();
    send();

    await screen.findByTestId("playground-error");
    expect(container.innerHTML).not.toContain(rawProviderText);
  });
});

describe("PlaygroundSurface — the key never leaves memory", () => {
  it("writes the key to no storage at all", async () => {
    const setItemSpy = vi.spyOn(Storage.prototype, "setItem");
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("ok")]))));
    renderSurface();
    enterKey();
    send();
    await waitFor(() => expect(screen.getByTestId("playground-answer").textContent).toBe("ok"));

    expect(setItemSpy).not.toHaveBeenCalled();
    const dump = JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } });
    expect(dump).not.toContain(REAL_KEY);
    expect(dump).not.toContain("vk_live_");
  });

  it("keeps the key out of the DOM outside its own masked input", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("ok")]))));
    const { container } = renderSurface();
    enterKey();
    send();
    await waitFor(() => expect(screen.getByTestId("playground-answer").textContent).toBe("ok"));

    // textContent excludes input values, so the key must be absent from it
    // entirely — including from the request preview, which shows the body only.
    expect(container.textContent ?? "").not.toContain(REAL_KEY);
    expect(screen.getByTestId("playground-request").textContent ?? "").not.toContain(REAL_KEY);
    // And the input is masked.
    expect((screen.getByTestId("playground-key") as HTMLInputElement).type).toBe("password");
  });

  it("never puts the key in the request URL", async () => {
    const fetchMock = vi.fn(async () => streamingResponse(sseStream([delta("ok")])));
    vi.stubGlobal("fetch", fetchMock);
    renderSurface();
    enterKey();
    send();

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const [url] = fetchMock.mock.calls[0] as unknown as [RequestInfo | URL];
    expect(String(url)).not.toContain(REAL_KEY);
    expect(String(url)).toBe("/v1/chat/completions");
  });

  it("clears the key on unmount", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("ok")]))));
    const { unmount, container } = renderSurface();
    enterKey();
    await waitFor(() =>
      expect((screen.getByTestId("playground-key") as HTMLInputElement).value).toBe(REAL_KEY),
    );

    unmount();
    // The whole subtree is gone, so no node can still hold the value.
    expect(container.innerHTML).toBe("");
  });
});

describe("PlaygroundSurface — accessibility", () => {
  it("has no axe violations before a send", async () => {
    vi.stubGlobal("fetch", vi.fn());
    const { container } = renderSurface();
    await assertNoAxeViolations(container);
  });

  it("has no axe violations with a completed response", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => streamingResponse(sseStream([delta("ok")]))));
    const { container } = renderSurface();
    enterKey();
    send();
    await waitFor(() => expect(screen.getByTestId("playground-answer").textContent).toBe("ok"));
    await assertNoAxeViolations(container);
  });
});

import "./lib/error-capture";

import { consumeLastCapturedError } from "./lib/error-capture";
import { renderErrorPage } from "./lib/error-page";
import { handleChatCompletions } from "./lib/api/chat-completions.server";
import { createLogger } from "./lib/logger";

const log = createLogger("server");

type ServerEntry = {
  fetch: (request: Request, env: unknown, ctx: unknown) => Promise<Response> | Response;
};

let serverEntryPromise: Promise<ServerEntry> | undefined;

async function getServerEntry(): Promise<ServerEntry> {
  if (!serverEntryPromise) {
    serverEntryPromise = import("@tanstack/react-start/server-entry").then(
      (m) => (m.default ?? m) as ServerEntry,
    );
  }
  return serverEntryPromise;
}

// h3 swallows in-handler throws into a normal 500 Response with body
// {"unhandled":true,"message":"HTTPError"} — try/catch alone never fires for those.
async function normalizeCatastrophicSsrResponse(response: Response): Promise<Response> {
  if (response.status < 500) return response;
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.includes("application/json")) return response;

  const body = await response.clone().text();
  if (!body.includes('"unhandled":true') || !body.includes('"message":"HTTPError"')) {
    return response;
  }

  const captured = consumeLastCapturedError();
  log.error("h3 swallowed SSR error", {
    body,
    error: captured instanceof Error ? captured.message : String(captured ?? ""),
  });
  return new Response(renderErrorPage(), {
    status: 500,
    headers: { "content-type": "text/html; charset=utf-8" },
  });
}

const DEV_WORKER_SECRET = process.env.DEV_WORKER_SECRET ?? "";

function addSecurityHeaders(response: Response): Response {
  const headers = new Headers(response.headers);
  // Content Security Policy — restrictive but allows inline styles for Tailwind/SSR
  headers.set(
    "Content-Security-Policy",
    [
      "default-src 'self'",
      "script-src 'self' 'wasm-unsafe-eval'", // wasm-unsafe-eval for WebAssembly if needed
      "style-src 'self' 'unsafe-inline'", // Tailwind v4 uses inline styles
      "img-src 'self' data: blob:",
      "font-src 'self' data:",
      "connect-src 'self' https://*.supabase.co wss://*.supabase.co", // Supabase realtime
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "form-action 'self'",
    ].join("; "),
  );
  // Additional hardening headers
  headers.set("X-Content-Type-Options", "nosniff");
  headers.set("X-Frame-Options", "DENY");
  headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=()");
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

export default {
  async fetch(request: Request, env: unknown, ctx: unknown) {
    try {
      const url = new URL(request.url);

      // ── Health check (no auth, lightweight) ───────────────────────────
      if (url.pathname === "/health" && request.method === "GET") {
        return addSecurityHeaders(
          new Response(JSON.stringify({ status: "ok", timestamp: new Date().toISOString() }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }

      // ── Venom proxy API ──────────────────────────────────────────────
      if (url.pathname === "/api/v1/chat/completions") {
        if (request.method === "OPTIONS") {
          return new Response(null, { status: 204, headers: { Allow: "POST, OPTIONS" } });
        }
        if (request.method !== "POST") {
          return new Response(
            JSON.stringify({
              error: {
                message: "Method not allowed",
                type: "invalid_request_error",
                code: "method_not_allowed",
              },
            }),
            {
              status: 405,
              headers: { "Content-Type": "application/json", Allow: "POST, OPTIONS" },
            },
          );
        }
        try {
          const res = await handleChatCompletions(request);
          return addSecurityHeaders(res);
        } catch (apiErr) {
          log.error("unhandled error in chat/completions", {
            error: apiErr instanceof Error ? apiErr.message : String(apiErr),
          });
          return addSecurityHeaders(
            new Response(
              JSON.stringify({
                error: {
                  message: "Internal server error.",
                  type: "server_error",
                  code: "internal_error",
                },
              }),
              { status: 500, headers: { "Content-Type": "application/json" } },
            ),
          );
        }
      }

      // ── Internal worker trigger (protected by DEV_WORKER_SECRET header) ──
      if (url.pathname === "/api/internal/run-workers" && request.method === "POST") {
        const secret = request.headers.get("x-worker-secret") ?? "";
        if (!DEV_WORKER_SECRET || secret !== DEV_WORKER_SECRET) {
          return addSecurityHeaders(
            new Response(JSON.stringify({ error: "Unauthorized" }), {
              status: 401,
              headers: { "Content-Type": "application/json" },
            }),
          );
        }
        try {
          const { runScheduled } = await import("./lib/workers/index.server");
          await runScheduled("manual");
          return addSecurityHeaders(
            new Response(JSON.stringify({ ok: true }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          );
        } catch (e: unknown) {
          const msg = String((e as { message?: string } | null)?.message ?? e);
          return addSecurityHeaders(
            new Response(JSON.stringify({ ok: false, error: msg }), {
              status: 500,
              headers: { "Content-Type": "application/json" },
            }),
          );
        }
      }

      // ── Public REST API (no auth) ────────────────────────────────────
      if (url.pathname === "/api/public/owner-exists" && request.method === "GET") {
        const { data } = await (
          await import("@/integrations/supabase/client.server")
        ).supabaseAdmin.auth.admin.listUsers({ perPage: 1, page: 1 });
        return addSecurityHeaders(
          new Response(JSON.stringify({ ownerExists: (data?.users?.length ?? 0) > 0 }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }

      // ── Dashboard REST API ───────────────────────────────────────────
      if (url.pathname.startsWith("/api/dashboard/")) {
        const { handleDashboardAPI } = await import("./lib/api/dashboard-router.server");
        const res = await handleDashboardAPI(request);
        if (res) return addSecurityHeaders(res);
      }

      // ── TanStack SSR ─────────────────────────────────────────────────
      const handler = await getServerEntry();
      const response = await handler.fetch(request, env, ctx);
      const normalized = await normalizeCatastrophicSsrResponse(response);
      return addSecurityHeaders(normalized);
    } catch (error) {
      log.error("server fetch failed", {
        error: error instanceof Error ? error.message : String(error),
      });
      return addSecurityHeaders(
        new Response(renderErrorPage(), {
          status: 500,
          headers: { "content-type": "text/html; charset=utf-8" },
        }),
      );
    }
  },

  // ── Cloudflare Workers cron handler ────────────────────────────────
  async scheduled(
    event: { cron: string; scheduledTime: number },
    _env: unknown,
    ctx: { waitUntil: (p: Promise<unknown>) => void },
  ) {
    ctx.waitUntil(
      import("./lib/workers/index.server").then(({ runScheduled }) => runScheduled(event.cron)),
    );
  },
};

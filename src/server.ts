import "./lib/error-capture";

import { consumeLastCapturedError } from "./lib/error-capture";
import { renderErrorPage } from "./lib/error-page";
import { handleChatCompletions } from "./lib/api/chat-completions.server";

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

  console.error(consumeLastCapturedError() ?? new Error(`h3 swallowed SSR error: ${body}`));
  return new Response(renderErrorPage(), {
    status: 500,
    headers: { "content-type": "text/html; charset=utf-8" },
  });
}

const DEV_WORKER_SECRET = process.env.DEV_WORKER_SECRET ?? "";

export default {
  async fetch(request: Request, env: unknown, ctx: unknown) {
    try {
      const url = new URL(request.url);

      // ── Venom proxy API ──────────────────────────────────────────────
      if (url.pathname === "/api/v1/chat/completions") {
        if (request.method === "OPTIONS") {
          return new Response(null, { status: 204, headers: { Allow: "POST, OPTIONS" } });
        }
        if (request.method !== "POST") {
          return new Response(
            JSON.stringify({ error: { message: "Method not allowed", type: "invalid_request_error", code: "method_not_allowed" } }),
            { status: 405, headers: { "Content-Type": "application/json", Allow: "POST, OPTIONS" } },
          );
        }
        try {
          return await handleChatCompletions(request);
        } catch (apiErr) {
          console.error("[venom/api] unhandled error in chat/completions:", apiErr);
          return new Response(
            JSON.stringify({ error: { message: "Internal server error.", type: "server_error", code: "internal_error" } }),
            { status: 500, headers: { "Content-Type": "application/json" } },
          );
        }
      }

      // ── Internal worker trigger (protected by DEV_WORKER_SECRET header) ──
      if (url.pathname === "/api/internal/run-workers" && request.method === "POST") {
        const secret = request.headers.get("x-worker-secret") ?? "";
        if (!DEV_WORKER_SECRET || secret !== DEV_WORKER_SECRET) {
          return new Response(JSON.stringify({ error: "Unauthorized" }), {
            status: 401,
            headers: { "Content-Type": "application/json" },
          });
        }
        try {
          const { runScheduled } = await import("./lib/workers/index.server");
          await runScheduled("manual");
          return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        } catch (e: unknown) {
          const msg = String((e as { message?: string } | null)?.message ?? e);
          return new Response(JSON.stringify({ ok: false, error: msg }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          });
        }
      }

      // ── Public REST API (no auth) ────────────────────────────────────
      if (url.pathname === "/api/public/owner-exists" && request.method === "GET") {
        const { data } = await (
          await import("@/integrations/supabase/client.server")
        ).supabaseAdmin.auth.admin.listUsers({ perPage: 1, page: 1 });
        return new Response(JSON.stringify({ ownerExists: (data?.users?.length ?? 0) > 0 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }

      // ── Dashboard REST API ───────────────────────────────────────────
      if (url.pathname.startsWith("/api/dashboard/")) {
        const { handleDashboardAPI } = await import("./lib/api/dashboard-router.server");
        const res = await handleDashboardAPI(request);
        if (res) return res;
      }

      // ── TanStack SSR ─────────────────────────────────────────────────
      const handler = await getServerEntry();
      const response = await handler.fetch(request, env, ctx);
      return await normalizeCatastrophicSsrResponse(response);
    } catch (error) {
      console.error(error);
      return new Response(renderErrorPage(), {
        status: 500,
        headers: { "content-type": "text/html; charset=utf-8" },
      });
    }
  },

  // ── Cloudflare Workers cron handler ────────────────────────────────
  async scheduled(
    event: { cron: string; scheduledTime: number },
    _env: unknown,
    ctx: { waitUntil: (p: Promise<unknown>) => void },
  ) {
    ctx.waitUntil(
      import("./lib/workers/index.server").then(({ runScheduled }) =>
        runScheduled(event.cron),
      ),
    );
  },
};

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

export default {
  async fetch(request: Request, env: unknown, ctx: unknown) {
    try {
      // Intercept Venom API routes before TanStack handles them
      const url = new URL(request.url);
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
};

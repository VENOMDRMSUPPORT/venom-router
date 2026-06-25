/**
 * Client-side error capture for React error boundaries.
 *
 * Records captured errors via the structured logger so they show up in the
 * browser console as leveled JSON (and can be shipped to a self-hosted
 * aggregator later). This is a private single-owner gateway — errors must
 * never be forwarded to an external SaaS. Swap in Sentry/self-hosted by
 * replacing the body of `reportClientError`.
 */
import { createLogger } from "@/lib/logger";

const log = createLogger("client-error");

export function reportClientError(error: unknown, context: Record<string, unknown> = {}) {
  const route =
    typeof window !== "undefined" && window.location ? window.location.pathname : undefined;
  log.error("Unhandled client error", { error: serializeError(error), route, ...context });
}

function serializeError(error: unknown): Record<string, unknown> {
  if (error instanceof Error) {
    return { name: error.name, message: error.message, stack: error.stack };
  }
  if (typeof error === "string") return { message: error };
  return { value: String(error) };
}

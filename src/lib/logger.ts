/**
 * Minimal structured logger usable from both server and client code.
 *
 * Emits leveled, JSON-ish lines that are easy to grep and ship to a
 * self-hosted log aggregator in production. It deliberately keeps no runtime
 * dependencies and never sends data off-process — this is a private
 * single-owner gateway, so errors must not be forwarded to any external SaaS.
 *
 * Secrets hygiene: callers must never pass raw credentials. The `redact`
 * helper exists for fields that are safe to log by key name.
 */

export type LogLevel = "debug" | "info" | "warn" | "error";

const LEVEL_ORDER: Record<LogLevel, number> = {
  debug: 10,
  info: 20,
  warn: 30,
  error: 40,
};

// Read the configured level from either the Vite client env (import.meta.env,
// statically replaced at build time) or the server runtime env (process.env).
// Client builds see only VITE_* vars; server sees the full process.env.
function readConfiguredLevel(): LogLevel {
  const raw =
    (import.meta as unknown as { env?: Record<string, string | undefined> }).env?.VITE_LOG_LEVEL ??
    (typeof process !== "undefined" ? process.env?.LOG_LEVEL : undefined) ??
    "info";
  const lowered = raw.toLowerCase() as LogLevel;
  return LEVEL_ORDER[lowered] !== undefined ? lowered : "info";
}

const ACTIVE_LEVEL = LEVEL_ORDER[readConfiguredLevel()];

const SENSITIVE_KEYS =
  /^(secret|password|token|api[_-]?key|key_hash|credentials|authorization|bearer)$/i;

function redact(value: unknown): unknown {
  if (value == null || typeof value !== "object") return value;
  if (Array.isArray(value)) return value.map(redact);
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    out[k] = SENSITIVE_KEYS.test(k) ? "[redacted]" : redact(v);
  }
  return out;
}

function emit(level: LogLevel, scope: string, message: string, meta?: Record<string, unknown>) {
  if (LEVEL_ORDER[level] < ACTIVE_LEVEL) return;
  const line = JSON.stringify({
    ts: new Date().toISOString(),
    level,
    scope,
    msg: message,
    ...(meta ? { ctx: redact(meta) } : {}),
  });
  // Respect the level on the actual sink so log aggregators can route by stream.
  if (level === "error") console.error(line);
  else if (level === "warn") console.warn(line);
  else console.log(line);
}

export interface Logger {
  debug: (message: string, meta?: Record<string, unknown>) => void;
  info: (message: string, meta?: Record<string, unknown>) => void;
  warn: (message: string, meta?: Record<string, unknown>) => void;
  error: (message: string, meta?: Record<string, unknown>) => void;
}

export function createLogger(scope: string): Logger {
  return {
    debug: (m, meta) => emit("debug", scope, m, meta),
    info: (m, meta) => emit("info", scope, m, meta),
    warn: (m, meta) => emit("warn", scope, m, meta),
    error: (m, meta) => emit("error", scope, m, meta),
  };
}

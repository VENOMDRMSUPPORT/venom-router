/* PKCE helpers + authorize URL builder — aligned with venom-router/lib/oauth/pkce.ts */
import crypto from "crypto";

export function generateCodeVerifier(): string {
  return crypto.randomBytes(32).toString("base64url");
}

export function generateCodeChallenge(verifier: string): string {
  return crypto.createHash("sha256").update(verifier).digest("base64url");
}

export function generateState(): string {
  return crypto.randomBytes(16).toString("hex");
}

/** Encode query params manually so spaces are %20 not + (required for Claude OAuth). */
export function buildOAuthAuthorizeUrl(baseUrl: string, params: Record<string, string>): string {
  const queryString = Object.entries(params)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&");
  return `${baseUrl}?${queryString}`;
}

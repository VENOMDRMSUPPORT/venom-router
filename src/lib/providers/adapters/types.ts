/* Shared types for provider adapters. Pure types — safe everywhere. */

export interface TokenSet {
  access_token: string;
  refresh_token?: string;
  expires_at?: number; // epoch ms
  scope?: string;
  raw?: Record<string, unknown>;
}

export interface AccountIdentity {
  email: string | null;
  plan: string | null;
  quota_used: number | null;
  quota_total: number | null;
  quota_unit: string | null;
  quota_extra?: Record<string, unknown>;
}

export interface DiscoveredModel {
  external_id: string;
  display_name: string;
  capabilities: string[];
}

export interface ModelTestResult {
  external_id: string;
  ok: boolean;
  latency_ms: number;
  error?: string;
}

export interface StoredCredentials {
  // Common: provider-specific token blob, encrypted at rest
  kind: "oauth2" | "api_key";
  // OAuth shape
  access_token?: string;
  refresh_token?: string;
  expires_at?: number;
  scope?: string;
  // For Antigravity: needed for all API calls
  project_id?: string;
  // API-key shape
  api_key?: string;
  // Any provider extras
  extra?: Record<string, unknown>;
}

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatRequest {
  messages: ChatMessage[];
  maxTokens?: number;
  temperature?: number;
}

export interface ChatResult {
  ok: boolean;
  content?: string;
  inputTokens: number;
  outputTokens: number;
  error?: string;
}

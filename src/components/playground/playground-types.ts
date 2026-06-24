import type { RoutingTrace } from "@/lib/routing/types";

export type VenomSlug = "lite" | "pro" | "max";

export type PlaygroundRequest = {
  venom_slug: VenomSlug;
  messages: { role: "user" | "assistant" | "system"; content: string }[];
  max_tokens?: number;
  temperature?: number;
};

export type PlaygroundResponse = {
  content: string;
  input_tokens: number;
  output_tokens: number;
  latency_ms: number;
  provider_adapter: string | null;
  fallback_used: boolean;
  fallback_count: number;
  cost_usd: number;
  modality: string;
  trace: RoutingTrace | null;
  request: PlaygroundRequest;
};

export type PlaygroundErrorPayload = {
  error: string;
  error_code?: string;
  error_message?: string;
  trace?: RoutingTrace | null;
  request?: PlaygroundRequest;
};

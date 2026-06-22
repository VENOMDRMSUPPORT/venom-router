/* Claude OAuth usage — ported from 9router open-sse/services/usage/claude.js */

const OAUTH_USAGE_URL = "https://api.anthropic.com/api/oauth/usage";
const SETTINGS_URL = "https://api.anthropic.com/v1/settings";
const API_VERSION = "2023-06-01";
const BETA_HEADER = "oauth-2025-04-20";
const OAUTH_429_COOLDOWN_MS = 180_000;

const oauthCooldown = new Map<string, number>();

export interface ClaudeQuotaWindow {
  used: number;
  total: number;
  remaining: number;
  remainingPercentage: number;
  resetAt?: string;
  unlimited: boolean;
}

export interface ClaudeUsageResult {
  plan: string;
  extraUsage?: unknown;
  quotas: Record<string, ClaudeQuotaWindow>;
  message?: string;
}

function oauthHeaders(token: string) {
  return {
    Authorization: `Bearer ${token}`,
    "anthropic-beta": BETA_HEADER,
    "anthropic-version": API_VERSION,
    Accept: "application/json",
  };
}

function hasUtilization(window: unknown): window is { utilization: number; resets_at?: string } {
  return (
    !!window &&
    typeof window === "object" &&
    typeof (window as { utilization?: unknown }).utilization === "number"
  );
}

function createQuotaObject(window: { utilization: number; resets_at?: string }): ClaudeQuotaWindow {
  const used = window.utilization;
  const remaining = Math.max(0, 100 - used);
  return {
    used,
    total: 100,
    remaining,
    remainingPercentage: remaining,
    resetAt: window.resets_at,
    unlimited: false,
  };
}

async function getClaudeUsageLegacy(accessToken: string): Promise<ClaudeUsageResult> {
  try {
    const settingsResponse = await fetch(SETTINGS_URL, {
      headers: oauthHeaders(accessToken),
      signal: AbortSignal.timeout(10_000),
    });
    if (!settingsResponse.ok) {
      return {
        plan: "Claude Code",
        quotas: {},
        message: "Claude connected. Usage API requires admin permissions.",
      };
    }
    const settings: any = await settingsResponse.json();
    if (settings.organization_id) {
      const usageResponse = await fetch(
        `https://api.anthropic.com/v1/organizations/${settings.organization_id}/usage`,
        { headers: oauthHeaders(accessToken), signal: AbortSignal.timeout(10_000) },
      );
      if (usageResponse.ok) {
        const usage = await usageResponse.json();
        return {
          plan: settings.plan || "Unknown",
          quotas: usage as Record<string, ClaudeQuotaWindow>,
        };
      }
    }
    return {
      plan: settings.plan || "Unknown",
      quotas: {},
      message: "Claude connected. Usage details require admin access.",
    };
  } catch (error: any) {
    return {
      plan: "Claude Code",
      quotas: {},
      message: `Claude connected. Unable to fetch usage: ${error.message}`,
    };
  }
}

export async function fetchClaudeUsage(accessToken: string): Promise<ClaudeUsageResult> {
  const cooldownUntil = oauthCooldown.get(accessToken);
  if (cooldownUntil && Date.now() < cooldownUntil) {
    return getClaudeUsageLegacy(accessToken);
  }

  try {
    const oauthResponse = await fetch(OAUTH_USAGE_URL, {
      headers: oauthHeaders(accessToken),
      signal: AbortSignal.timeout(10_000),
    });

    if (oauthResponse.ok) {
      const data: any = await oauthResponse.json();
      const quotas: Record<string, ClaudeQuotaWindow> = {};

      if (hasUtilization(data.five_hour)) {
        quotas["session (5h)"] = createQuotaObject(data.five_hour);
      }
      if (hasUtilization(data.seven_day)) {
        quotas["weekly (7d)"] = createQuotaObject(data.seven_day);
      }
      for (const [key, value] of Object.entries(data)) {
        if (key.startsWith("seven_day_") && key !== "seven_day" && hasUtilization(value)) {
          const modelName = key.replace("seven_day_", "");
          quotas[`weekly ${modelName} (7d)`] = createQuotaObject(value);
        }
      }

      return { plan: "Claude Code", extraUsage: data.extra_usage ?? null, quotas };
    }

    if (oauthResponse.status === 429) {
      oauthCooldown.set(accessToken, Date.now() + OAUTH_429_COOLDOWN_MS);
    }

    return getClaudeUsageLegacy(accessToken);
  } catch (error: any) {
    return {
      plan: "Claude Code",
      quotas: {},
      message: `Claude connected. Unable to fetch usage: ${error.message}`,
    };
  }
}

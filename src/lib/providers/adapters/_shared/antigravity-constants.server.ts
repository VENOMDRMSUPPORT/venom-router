/* Antigravity API constants — aligned with venom-router/lib/adapters/antigravity/constants.ts */

export const ANTIGRAVITY_BASE = "https://cloudcode-pa.googleapis.com";
export const ANTIGRAVITY_USER_AGENT = "antigravity/1.107.0 darwin/arm64";
export const GOOGLE_USERINFO_URL = "https://www.googleapis.com/oauth2/v2/userinfo";

export const OAUTH_CLIENT_METADATA = {
  ideType: "ANTIGRAVITY",
  platform: "PLATFORM_UNSPECIFIED",
  pluginType: "GEMINI",
} as const;

/** Request body for loadCodeAssist — metadata nested, no `mode` field. */
export function loadCodeAssistBody() {
  return {
    metadata: {
      ideType: OAUTH_CLIENT_METADATA.ideType,
      platform: OAUTH_CLIENT_METADATA.platform,
      pluginType: OAUTH_CLIENT_METADATA.pluginType,
    },
  };
}

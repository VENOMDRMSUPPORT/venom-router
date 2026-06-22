/**
 * Static verification report for Antigravity Live Model Discovery modal.
 * Run: bun run test:verify-antigravity-modal
 */
import {
  buildAntigravityLiveFetchSnapshot,
  mergeDbOverlay,
  type AntigravityLiveModelEntry,
} from "../src/lib/providers/antigravity-live-snapshot.ts";
import { providerExternalId } from "../src/lib/providers/model-keys.ts";

const RECOMMENDED_IDS = [
  "gemini-3.5-flash-medium",
  "gemini-3.5-flash-high",
  "gemini-3.5-flash-low",
  "gemini-3.1-pro-low",
  "gemini-3.1-pro-high",
  "claude-sonnet-4.6-thinking",
  "claude-opus-4.6-thinking",
  "gpt-oss-120b-medium",
];

const LIVE_MODELS: Record<string, AntigravityLiveModelEntry> = {
  "gemini-3.5-flash-medium": {
    displayName: "Gemini 3.5 Flash (Medium) - Fast",
    supportsImages: true,
    quotaInfo: { remainingFraction: 0.72, resetTime: "2026-06-22T18:00:00Z" },
  },
  "gemini-3.5-flash-high": {
    displayName: "Gemini 3.5 Flash (High) - Fast",
    quotaInfo: { remainingFraction: 0.65, resetTime: "2026-06-22T18:00:00Z" },
  },
  "gemini-3.5-flash-low": {
    displayName: "Gemini 3.5 Flash (Low) - Fast",
    quotaInfo: { remainingFraction: 0.8, resetTime: "2026-06-22T18:00:00Z" },
  },
  "gemini-3.1-pro-low": { displayName: "Gemini 3.1 Pro (Low)" },
  "gemini-3.1-pro-high": { displayName: "Gemini 3.1 Pro (High)" },
  "claude-sonnet-4.6-thinking": {
    displayName: "Claude Sonnet 4.6 (Thinking)",
    supportsThinking: true,
  },
  "claude-opus-4.6-thinking": {
    displayName: "Claude Opus 4.6 (Thinking)",
    supportsThinking: true,
  },
  "gpt-oss-120b-medium": { displayName: "GPT-OSS 120B (Medium)" },
  chat_20706: { displayName: "chat_20706" },
  chat_23310: { displayName: "chat_23310" },
  tab_jump_flash_lite_preview: { displayName: "Tab Jump Flash Lite Preview" },
};

const DB_LEGACY_ONLY = [
  {
    id: "legacy-chat-1",
    external_id: "chat_20706",
    display_name: "chat_20706",
    test_status: "working",
  },
  {
    id: "legacy-chat-2",
    external_id: "chat_23310",
    display_name: "chat_23310",
    test_status: "failed",
  },
  { id: "other-provider", external_id: "claude-sonnet-from-other", display_name: "Foreign Model" },
];

const rawResponse = {
  models: LIVE_MODELS,
  agentModelSorts: {
    Recommended: { groups: [{ modelIds: RECOMMENDED_IDS }] },
  },
};

const snapshot = buildAntigravityLiveFetchSnapshot({
  rawResponse,
  projectId: "demo-project-123",
  planTier: "Pro",
  persistenceStats: {
    insertedNewCount: 0,
    updatedExistingCount: Object.keys(LIVE_MODELS).length,
    unchangedCount: 0,
  },
});

const visible = mergeDbOverlay(snapshot.visibleCatalog.models, DB_LEGACY_ONLY, providerExternalId);

console.log("=== Antigravity Live Modal Verification Report ===\n");
console.log("Summary counts:");
console.log(`  Raw fetched: ${snapshot.stats.rawFetchedCount}`);
console.log(`  IDE-visible: ${snapshot.stats.visibleCount}`);
console.log(`  Inserted new: ${snapshot.stats.insertedNewCount}`);
console.log(`  Updated existing: ${snapshot.stats.updatedExistingCount}`);
console.log(`  Recommended sort found: ${snapshot.diagnostics.recommendedSortFound}`);
console.log(`  Duplicate prevented: ${snapshot.stats.duplicatePreventedCount ?? 0}`);
console.log(`  Stale removed from active: ${snapshot.stats.removedStaleCount ?? 0}`);

console.log("IDE-visible models in main list:");
for (const m of visible) {
  console.log(`  - ${m.displayName} (${m.id})`);
}

console.log("\nRaw catalog (diagnostics only):");
for (const m of snapshot.rawCatalog.models) {
  console.log(`  - ${m.id}`);
}

const chatInModal = visible.filter((m) => m.id.startsWith("chat_"));
console.log(`\nchat_* entries in main list: ${chatInModal.length} (expected 0)`);
console.log(`Raw catalog count: ${snapshot.rawCatalog.count} (expected 11)`);
console.log(`Visible catalog count: ${snapshot.visibleCatalog.count} (expected 8)`);

console.log("\n=== End Report ===");

if (visible.length !== RECOMMENDED_IDS.length) process.exit(1);
if (chatInModal.length > 0) process.exit(1);
if (snapshot.rawCatalog.count !== Object.keys(LIVE_MODELS).length) process.exit(1);
if (snapshot.stats.visibleCount !== RECOMMENDED_IDS.length) process.exit(1);

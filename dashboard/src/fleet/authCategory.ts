/**
 * The Providers page's auth-mode filter — its own module rather than an
 * export from FleetOverview.tsx, because exporting non-components from a
 * component file breaks react-refresh (the same reason quotaWindows.ts
 * exists). Both the page's tabs and the shell's breadcrumb read from here,
 * so the two can never disagree about the filter's spelling.
 */

/** The auth-mode filter the segmented tabs select. The two tabs PARTITION
 * the catalog — every provider belongs to exactly one — so there is no "all"
 * member and no provider can be unreachable. That totality is why
 * `matchesAuthCategory` tests api_key as the COMPLEMENT of oauth rather than
 * by equality: `custom_openai` (a custom OpenAI-compatible endpoint, which is
 * authenticated with a key) was previously visible only under an "All" tab. */
export type AuthCategory = "oauth" | "api_key";

/** The tab labels. The breadcrumb's third segment renders these VERBATIM —
 * two spellings of one filter read as two different destinations. */
export const CATEGORY_OPTIONS: { value: AuthCategory; label: string }[] = [
  { value: "oauth", label: "OAuth Providers" },
  { value: "api_key", label: "API Key Providers" },
];

/** The tab the page opens on: OAuth is where the owner's real fleet lives. */
export const DEFAULT_AUTH_CATEGORY: AuthCategory = "oauth";

/** The human label for a category, used by the tab-scoped empty states. */
export function authCategoryLabel(category: AuthCategory): string {
  return CATEGORY_OPTIONS.find((option) => option.value === category)?.label ?? "providers";
}

/** Whether a provider belongs to the given tab.
 *
 * The api_key branch is deliberately the COMPLEMENT of the OAuth test, not
 * `=== "api_key"`: every non-OAuth auth mode is key-authenticated
 * (`api_key`, `custom_openai`), so the two tabs stay total and a provider
 * can never fall through both. An equality test here would silently hide
 * `custom_openai` now that there is no "All" tab to catch it. */
export function matchesAuthCategory(authMode: string, category: AuthCategory): boolean {
  return category === "oauth" ? authMode === "oauth2" : authMode !== "oauth2";
}

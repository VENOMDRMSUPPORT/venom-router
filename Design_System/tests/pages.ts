// Shared page inventory for the a11y + visual suites. Kept in one place so both suites
// exercise the same "every card / every state matrix / representative compositions" set
// the remediation brief asks for.
export const THEMES = ["venom-dark", "venom-light"] as const;
export const DENSITIES = ["comfortable", "compact"] as const;

export const PRIMITIVE_CARDS = [
  "/components/icons/icon.card.html",
  "/components/actions/actions.card.html",
  "/components/forms/forms.card.html",
  "/components/display/display.card.html",
  "/components/feedback/feedback.card.html",
  "/components/navigation/navigation.card.html",
  "/components/containers/containers.card.html",
  "/components/overlay/overlay.card.html",
  "/components/data/data.card.html",
];

export const DOMAIN_CARDS = [
  "/components/domain-provider/provider.card.html",
  "/components/domain-model/model.card.html",
  "/components/domain-quota/quota.card.html",
  "/components/domain-routing/routing.card.html",
  "/components/domain-security/security.card.html",
  "/components/domain-diagnostics/diagnostics.card.html",
];

export const STATE_MATRICES = [
  "/states/account-lifecycle.html",
  "/states/credential-lifecycle.html",
  "/states/funding-evidence.html",
  "/states/capability-truth.html",
  "/states/certification.html",
  "/states/quota-freshness.html",
  "/states/reservation-reconciliation.html",
  "/states/routing-outcomes.html",
  "/states/owner-authentication.html",
  "/states/async-jobs.html",
];

export const FOUNDATIONS = [
  "/foundations/colors-surfaces.html",
  "/foundations/colors-status.html",
  "/foundations/colors-accent.html",
  "/foundations/colors-tiers.html",
  "/foundations/colors-viz.html",
  "/foundations/type-ui.html",
  "/foundations/type-mono.html",
  "/foundations/type-data.html",
  "/foundations/spacing.html",
  "/foundations/radius-borders.html",
  "/foundations/elevation.html",
  "/foundations/motion.html",
  "/foundations/density.html",
  "/foundations/focus.html",
  "/foundations/icons.html",
];

export const COMPOSITIONS = ["/ui_kits/venom-console/index.html"];

export const ALL_CARD_PAGES = [...PRIMITIVE_CARDS, ...DOMAIN_CARDS];

/** The 17 console screens, addressed by hash — see ui_kits/venom-console/screen-registry.json. */
export const CONSOLE_SCREENS = [
  "overview",
  "providers",
  "provider-detail",
  "models",
  "routing",
  "routing-rules",
  "playground",
  "usage",
  "quota",
  "token-health",
  "diagnostics",
  "tier-status",
  "api-keys",
  "settings",
  "login",
  "first-run",
  "backup",
];

export async function setThemeDensity(page: import("@playwright/test").Page, theme: string, density = "comfortable") {
  await page.evaluate(
    ([t, d]) => {
      document.documentElement.setAttribute("data-theme", t as string);
      document.documentElement.setAttribute("data-density", d as string);
    },
    [theme, density]
  );
}

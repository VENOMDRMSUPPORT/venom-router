import axe from "axe-core";

/**
 * Runs axe-core over `container` and throws (with a readable summary) if
 * it finds any violation. Used to prove the first-run, login, and
 * locked-out states render accessibly (P2b-UI-002's DoD).
 *
 * `color-contrast` is disabled: jsdom has no real layout/paint engine, so
 * it cannot compute actual rendered colors, and axe-core's contrast rule
 * needs those — this is the standard accommodation for running axe-core
 * under jsdom rather than a real browser. Every other rule (labels,
 * roles, aria-*, focus order via DOM structure, etc.) still runs at full
 * strength. A full-browser accessibility pass (including real contrast
 * checks) belongs to a future in-browser acceptance suite, not this
 * Vitest/jsdom one.
 */
export async function assertNoAxeViolations(container: Element): Promise<void> {
  const results = await axe.run(container, {
    rules: { "color-contrast": { enabled: false } },
  });

  if (results.violations.length > 0) {
    const details = results.violations
      .map((violation) => {
        const targets = violation.nodes.map((node) => node.target.join(" ")).join(", ");
        return `- [${violation.id}] ${violation.help} (${targets})`;
      })
      .join("\n");
    throw new Error(`axe-core found ${results.violations.length} accessibility violation(s):\n${details}`);
  }
}

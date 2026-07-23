/**
 * Public entry point: the generated Tailwind theme extension.
 * `venomTailwindTheme` (a `theme.extend` object) and `venomTailwindPreset` (a drop-in
 * Tailwind preset) are compiled by `validation/build-tokens.cjs` from the same authored
 * `tokens/*.json` as the CSS custom properties and the typed tokens object — the Tailwind
 * mapping is never authored by hand, here or in a consuming app. Every value is a
 * `var(--…)` reference to the generated CSS custom properties, so utilities re-resolve
 * under `data-theme` / `data-density` at runtime (breakpoints are the one literal
 * exception — media queries cannot read CSS custom properties).
 *
 * Consume from `tailwind.config`:
 *   import { venomTailwindPreset } from "@venom/design-system/tailwind";
 *   export default { presets: [venomTailwindPreset], content: [...] };
 */
export { venomTailwindTheme, venomTailwindPreset } from "../tokens/tailwind-theme";
export type { VenomTailwindTheme } from "../tokens/tailwind-theme";

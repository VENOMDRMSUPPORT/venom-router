import { venomTailwindPreset } from "@venom/design-system/tailwind";

// The token→Tailwind mapping comes solely from venomTailwindPreset — a
// generated artifact of @venom/design-system, compiled from the same
// authored tokens/*.json as the CSS custom properties (07 §2.3). Never
// hand-author or duplicate that mapping here.
/** @type {import('tailwindcss').Config} */
export default {
  presets: [venomTailwindPreset],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
};

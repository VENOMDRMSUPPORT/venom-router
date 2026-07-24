import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

// P2a-DS-003: the no-raw-values guardrail (07 §8 — "an ESLint/Stylelint
// rule bans hex/rgb/hsl colors, raw px for spacing, raw shadows, and
// off-scale numbers"). Implemented with ESLint core's no-restricted-syntax
// (no extra lint-plugin dependency needed) so every value must come from a
// venomTailwindPreset-mapped utility class instead. Each pattern is banned
// in both plain string literals and template-literal segments, since a raw
// value can hide in either. CI-blocking: severity is "error", not "warn".
const RAW_VALUE_PATTERNS = [
  {
    regex: "#(?:[0-9a-fA-F]{3,4}){1,2}\\b|\\b(?:rgba?|hsla?)\\(",
    message:
      "Raw color value (hex/rgb/hsl) is forbidden — use a design token (a venomTailwindPreset-mapped utility class, which resolves to var(--...)) instead.",
  },
  {
    regex: "-\\[[^\\]]+\\]",
    message:
      "Tailwind arbitrary-value syntax (e.g. p-[13px], bg-[#fff], shadow-[...]) is forbidden — it bypasses the token scale. Use a preset-mapped utility class instead.",
  },
  {
    regex: "\\b\\d+px\\b",
    message: "Raw px value is forbidden — use a spacing/sizing token (a preset-mapped utility class) instead.",
  },
];

const noRawValuesRules = RAW_VALUE_PATTERNS.flatMap(({ regex, message }) => [
  { selector: `Literal[value=/${regex}/]`, message },
  { selector: `TemplateElement[value.raw=/${regex}/]`, message },
]);

export default tseslint.config(
  { ignores: ["dist"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
      "no-restricted-syntax": ["error", ...noRawValuesRules],
    },
  },
);

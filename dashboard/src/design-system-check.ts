// P2a-DS-001 scope only: proves `@venom/design-system` resolves and type-checks
// as a `file:` dependency. Real theme/token wiring is DS-002, not here.
import { DEFAULT_THEME, THEMES, type ThemeName } from "@venom/design-system/themes";

export const designSystemCheck: { defaultTheme: ThemeName; themes: readonly ThemeName[] } = {
  defaultTheme: DEFAULT_THEME,
  themes: THEMES,
};

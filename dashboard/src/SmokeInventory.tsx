import { useState } from "react";
import { Button, DensityToggle, Table, ThemeSwitcher } from "@venom/design-system/primitives";
import { AccountStatus } from "@venom/design-system/domain";
import { DEFAULT_DENSITY, DEFAULT_THEME, setDensity, setTheme, type DensityName, type ThemeName } from "./theme-runtime";

// P2a-DS-004: composes representative REAL @venom/design-system components —
// two primitives (Button, Table), the real ThemeSwitcher/DensityToggle
// (also primitives), and one domain component (AccountStatus) — to prove
// the package renders correctly end-to-end across all 3 themes x 2
// densities (07 §3/§8/§10). Not a production surface; that's later work.
export default function SmokeInventory() {
  const [theme, setThemeState] = useState<ThemeName>(DEFAULT_THEME);
  const [density, setDensityState] = useState<DensityName>(DEFAULT_DENSITY);

  return (
    <section aria-label="Design system smoke inventory">
      <h1>Design system smoke inventory</h1>

      <ThemeSwitcher
        value={theme}
        onChange={(next) => {
          setTheme(next);
          setThemeState(next);
        }}
      />
      <DensityToggle
        value={density}
        onChange={(next) => {
          setDensity(next);
          setDensityState(next);
        }}
      />

      <Button>Primitive button</Button>

      <AccountStatus status="healthy" />

      <Table label="Smoke table">
        <tbody>
          <tr>
            <td>example-provider</td>
            <td>healthy</td>
          </tr>
        </tbody>
      </Table>
    </section>
  );
}

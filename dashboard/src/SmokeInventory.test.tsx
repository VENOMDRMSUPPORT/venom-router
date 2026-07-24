import { afterEach, describe, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { DENSITIES, DENSITY_LABELS, THEMES, THEME_LABELS } from "./theme-runtime";
import SmokeInventory from "./SmokeInventory";

afterEach(() => {
  cleanup();
});

// All 6 theme x density combinations (07 §3/§8/§10, P2a-DS-004's DoD:
// "inventory renders in all 3 themes" x both densities).
const COMBINATIONS = THEMES.flatMap((theme) => DENSITIES.map((density) => ({ theme, density })));

describe("SmokeInventory — real @venom/design-system components render across the full theme x density matrix", () => {
  it.each(COMBINATIONS)("applies theme=$theme density=$density via the real ThemeSwitcher/DensityToggle", ({ theme, density }) => {
    render(<SmokeInventory />);

    // Drive the switch through the REAL package components (ThemeSwitcher /
    // DensityToggle), not by poking the DOM directly — this is the
    // end-to-end proof the card asks for.
    fireEvent.click(screen.getByRole("radio", { name: THEME_LABELS[theme] }));
    fireEvent.click(screen.getByRole("radio", { name: DENSITY_LABELS[density] }));

    if (document.documentElement.getAttribute("data-theme") !== theme) {
      throw new Error(
        `data-theme = ${document.documentElement.getAttribute("data-theme")}, want ${theme}`,
      );
    }
    if (document.documentElement.getAttribute("data-density") !== density) {
      throw new Error(
        `data-density = ${document.documentElement.getAttribute("data-density")}, want ${density}`,
      );
    }

    // The rest of the inventory (>=1 primitive + >=1 domain component)
    // must still be mounted after the switch — getBy* throws if missing,
    // which is the "renders without error" assertion.
    screen.getByRole("button", { name: /primitive button/i });
    screen.getByRole("table", { name: /smoke table/i });
    screen.getByTitle("display_status: healthy"); // the domain component (AccountStatus)
  });
});

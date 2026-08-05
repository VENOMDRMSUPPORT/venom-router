import { useState } from "react";
import { Button, DensityToggle, Table, ThemeSwitcher, ToastProvider, toast } from "@venom/design-system/primitives";
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

  const triggerPromiseToast = () => {
    toast.promise(
      new Promise((resolve) => setTimeout(resolve, 2000)),
      {
        loading: "Saving system settings...",
        success: "System settings saved!",
        error: "Failed to save settings.",
      }
    );
  };

  return (
    <ToastProvider>
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

        <div style={{ marginTop: 16, display: "flex", gap: 8, flexWrap: "wrap" }}>
          <Button
            onClick={() =>
              toast.success("Healthy Toast", {
                detail: "Operation completed successfully",
                action: { label: "Undo", onClick: () => alert("Undo action clicked") },
              })
            }
          >
            Toast Success
          </Button>
          <Button
            onClick={() =>
              toast.danger("Critical Toast", {
                detail: "System failure encountered",
                action: { label: "Retry", onClick: () => alert("Retry action clicked") },
              })
            }
          >
            Toast Danger
          </Button>
          <Button
            onClick={() =>
              toast.warning("Warning Toast", {
                detail: "Resource consumption at 85%",
                action: { label: "Inspect", onClick: () => alert("Inspect action clicked") },
              })
            }
          >
            Toast Warning
          </Button>
          <Button
            onClick={() =>
              toast.info("Info Toast", {
                detail: "New features deployed",
                action: { label: "Read docs", onClick: () => alert("Read docs action clicked") },
              })
            }
          >
            Toast Info
          </Button>
          <Button
            onClick={() =>
              toast.loading("Loading Toast", {
                detail: "Synchronizing data...",
              })
            }
          >
            Toast Loading
          </Button>
          <Button onClick={triggerPromiseToast}>
            Toast Promise
          </Button>
          <Button
            onClick={() =>
              toast.custom("Custom Toast", {
                detail: "Custom toast notification",
                tone: "custom",
              })
            }
          >
            Toast Custom
          </Button>
        </div>
      </section>
    </ToastProvider>
  );
}

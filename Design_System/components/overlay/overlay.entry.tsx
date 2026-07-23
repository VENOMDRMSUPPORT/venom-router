// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Tooltip, Popover, DropdownMenu, Dialog, Drawer, Sheet, Button, IconButton, Badge, FormField, Input, KeyValueList } from "../../src/index";

function Card() {

  const [dlg, setDlg] = React.useState(false);
  const [drawer, setDrawer] = React.useState(false);
  const [sheet, setSheet] = React.useState(false);
  const [sheetAlias, setSheetAlias] = React.useState(false);
  const nameRef = React.useRef<HTMLInputElement>(null);
  return (
    <div>
      <div className="row">
        <Tooltip content="Verified by probe · observed 2h ago · confidence 0.95">
          <span className="vn-badge vn-badge--mono" tabIndex={0}>1M ctx</span>
        </Tooltip>
        <Popover trigger={<Button size="sm">Funding evidence</Button>}>
          <div className="vn-body-compact" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            <Badge tone="healthy" icon="hand-coins">Free</Badge>
            <span className="vn-caption">source: provider_evidence · confidence 0.95 · 2026-07-22</span>
          </div>
        </Popover>
        <DropdownMenu trigger={<IconButton icon="ellipsis" label="Account actions" variant="secondary" />} items={[
          { label: "Refresh health", icon: "heart-pulse" },
          { label: "Run discovery", icon: "box" },
          { label: "Reveal credential", icon: "eye", kbd: "R" },
          { label: "Reauthenticate", icon: "fingerprint", disabled: true },
          { type: "separator" },
          { label: "Stop routing", icon: "power" },
          { label: "Disconnect", icon: "unplug", danger: true },
        ]} />
        <Button variant="primary" size="sm" onClick={() => setDlg(true)}>Open dialog</Button>
        <Button size="sm" icon="box" onClick={() => setDrawer(true)}>Open drawer</Button>
        <Button size="sm" icon="user-round" onClick={() => setSheet(true)}>Open sheet (initial focus)</Button>
        <Button size="sm" icon="badge-check" onClick={() => setSheetAlias(true)}>Open Sheet (alias)</Button>
      </div>
      <p className="vn-caption">Menus: Arrow keys + Home/End + Enter + Escape, roving DOM focus. Dialogs/Drawers trap focus and restore it on close. AlertDialog blocks until an explicit choice.</p>
      <Dialog open={dlg} onClose={() => setDlg(false)} title="Connect OpenCode Zen"
        footer={<React.Fragment><Button onClick={() => setDlg(false)}>Cancel</Button><Button variant="primary">Validate & connect</Button></React.Fragment>}>
        <FormField label="API key" description="Validated with a zero-cost chat probe — a 200 on /v1/models proves nothing.">
          <Input mono placeholder="Paste key" />
        </FormField>
      </Dialog>
      <Drawer open={drawer} onClose={() => setDrawer(false)} title="claude-sonnet-4-5" description="claude-code · ops@venom.local"
        footer={<Button variant="primary" onClick={() => setDrawer(false)}>Done</Button>}>
        <KeyValueList items={[{ key: "Context", value: "1,000,000", mono: true }, { key: "Certification", value: "certified" }]} />
      </Drawer>
      <Drawer open={sheet} onClose={() => setSheet(false)} title="Edit account" initialFocusRef={nameRef}>
        <FormField label="Display name"><Input ref={nameRef} defaultValue="ops@venom.local" /></FormField>
      </Drawer>
      <Sheet open={sheetAlias} onClose={() => setSheetAlias(false)} title="Sheet alias" footer={<Button variant="primary" onClick={() => setSheetAlias(false)}>Dismiss</Button>}>
        <p className="vn-caption">Sheet is a plain alias of Drawer — same focus trap, same restore-on-close.</p>
      </Sheet>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

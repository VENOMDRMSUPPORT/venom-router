// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Card, StatCard, Panel, Accordion, IconButton } from "../../src/index";

function CardSpecimen() {

  return (
    <div className="stack">
      <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 12 }}>
        <StatCard label="Providers" value="7" meta="of 11 configured" />
        <StatCard label="Accounts" value="14" meta="12 connected" />
        <StatCard label="Healthy" value="12" meta="1 degraded · 1 expired" tone="healthy" />
        <StatCard label="Certified offerings" value="86" meta="9 in review queue" />
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
        <Panel title="Quota windows" actions={<IconButton icon="refresh-cw" label="Refresh" size="sm" />}>
          <div style={{ padding: "var(--space-3) var(--space-4)" }} className="vn-body-compact vn-text-muted">Panel body — the default grouping surface.</div>
        </Panel>
        <Accordion defaultOpen={[0]} items={[
          { title: "Advanced options", content: "Custom headers are stored encrypted; names only appear in settings." },
          { title: "Owner overrides", content: "Overrides are never auto-superseded by provider evidence." },
        ]} />
      </div>
      <div style={{ display: "flex", gap: 12 }}>
        <Card interactive style={{ flex: 1 }}><span className="vn-body-compact">Interactive card (hover)</span></Card>
        <Card selected style={{ flex: 1 }}><span className="vn-body-compact">Selected card</span></Card>
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<CardSpecimen />);

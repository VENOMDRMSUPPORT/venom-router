// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { DataTable, Badge, EmptyState } from "../../src/index";

function Card() {

  const [sort, setSort] = React.useState({ key: "ctx", dir: "desc" });
  const rows = [
    { id: 1, model: "claude-sonnet-4-5", provider: "claude-code", ctx: 1000000, cert: "certified", funding: "paid" },
    { id: 2, model: "gpt-5.2-codex", provider: "codex", ctx: 400000, cert: "certified", funding: "paid" },
    { id: 3, model: "glm-5-free", provider: "opencode-zen", ctx: 256000, cert: "probing", funding: "free" },
    { id: 4, model: "kimi-k2.5", provider: "agnes-ai", ctx: null, cert: "observed", funding: "unknown" },
  ];
  const certTone = { certified: "accent", probing: "info", observed: "info" };
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <DataTable label="Offerings" rowKey="id" selectedKey={1} sort={sort} onSort={setSort} onRowClick={() => {}}
        columns={[
          { key: "model", label: "Model", mono: true, sortable: true },
          { key: "provider", label: "Provider", mono: true },
          { key: "ctx", label: "Context", numeric: true, sortable: true, render: (r) => r.ctx == null ? <Badge tone="unknown" icon="circle-help">unknown</Badge> : r.ctx.toLocaleString() },
          { key: "cert", label: "Certification", render: (r) => <Badge tone={certTone[r.cert] || "inactive"} icon={r.cert === "certified" ? "badge-check" : "flask-conical"}>{r.cert}</Badge> },
          { key: "funding", label: "Funding", render: (r) => <Badge tone={r.funding === "free" ? "healthy" : r.funding === "paid" ? "info" : "unknown"} icon={r.funding === "free" ? "hand-coins" : r.funding === "paid" ? "credit-card" : "circle-help"}>{r.funding}</Badge> },
        ]}
        rows={rows} />
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
        <DataTable label="Loading" loading columns={[{ key: "a", label: "Model" }, { key: "b", label: "Context", numeric: true }]} rows={[]} />
        <DataTable label="Empty" columns={[{ key: "a", label: "Model" }, { key: "b", label: "Context", numeric: true }]} rows={[]}
          empty={<EmptyState icon="box" title="No offerings discovered" description="Run discovery on a connected account." />} />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

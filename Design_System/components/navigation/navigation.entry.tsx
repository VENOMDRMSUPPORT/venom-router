// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Tabs, SegmentedControl, Breadcrumbs, Pagination } from "../../src/index";

function Card() {

  const [tab, setTab] = React.useState("offerings");
  const [density, setDensity] = React.useState("Comfortable");
  const [page, setPage] = React.useState(2);
  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "Providers", href: "#" }, { label: "claude-code", href: "#" }, { label: "ops@venom.local" }]} />
      <Tabs label="Account detail" value={tab} onChange={setTab} tabs={[
        { value: "offerings", label: "Offerings", count: 12 },
        { value: "quota", label: "Quota", count: 4 },
        { value: "credentials", label: "Credentials" },
        { value: "audit", label: "Audit" },
      ]} />
      <div style={{ display: "flex", gap: 16, alignItems: "center", flexWrap: "wrap" }}>
        <SegmentedControl label="Density" options={["Comfortable", "Compact"]} value={density} onChange={setDensity} />
        <SegmentedControl label="Theme" options={["Dark", "Light", "HC"]} value="Dark" onChange={() => {}} />
        <Pagination page={page} pageCount={25} rangeLabel="51–100 of 1,204" onPage={setPage} />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

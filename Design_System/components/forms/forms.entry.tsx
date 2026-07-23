// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { FormField, Input, Select, Combobox, Checkbox, RadioGroup, Switch, Slider, SearchField, Textarea, FilterBar, SegmentedControl } from "../../src/index";

function Card() {

  const [funding, setFunding] = React.useState("free");
  const [model, setModel] = React.useState("");
  const [q, setQ] = React.useState("");
  const [view, setView] = React.useState("All");
  return (
    <div className="grid">
      <div style={{ gridColumn: "1 / -1" }}>
        <FilterBar searchValue={q} onSearchChange={setQ} searchPlaceholder="Filter by model, provider, account"
          activeFilters={[{ key: "funding", label: "Funding", value: "free", onClear: () => {} }]}
          onClearAll={() => {}}>
          <SegmentedControl label="View" options={["All", "Routable", "Review queue"]} value={view} onChange={setView} />
        </FilterBar>
      </div>
      <FormField label="API key" required description="Validated with a zero-cost chat probe. 429/5xx means provider-unavailable, not an invalid key.">
        <Input mono placeholder="Paste the provider API key" />
      </FormField>
      <FormField label="Base URL" error="A base URL must use https.">
        <Input mono defaultValue="http://api.example.dev/v1" />
      </FormField>
      <FormField label="Funding classification" description="Funding is a fact about this account — never the provider.">
        <Select options={["free", "paid", "unknown"]} defaultValue="free" />
      </FormField>
      <FormField label="Model" description="Searchable — options come from account-scoped discovery.">
        <Combobox options={["claude-sonnet-4-5", "gpt-5.2-codex", "grok-4-fast", "glm-5-free", "kimi-k2.5"]} value={model} onChange={setModel} />
      </FormField>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        <Checkbox label="Include withdrawn offerings" defaultChecked />
        <Checkbox label="Indeterminate (bulk header)" indeterminate />
        <Checkbox label="Disabled" disabled />
        <Switch label="Enable metadata enrichment (off by default)" />
        <Switch label="Expensive probes" defaultChecked />
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <RadioGroup name="f" value={funding} onChange={setFunding} label="Funding" options={[
          { value: "free", label: "Free — verified zero marginal cost" },
          { value: "paid", label: "Paid" },
          { value: "unknown", label: "Unknown — excluded from routing until classified" },
        ]} />
        <Slider min={0} max={120} defaultValue={60} unit=" rpm" label="Per-key RPM limit" />
        <SearchField placeholder="Filter accounts" />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { ModelIdentity, ModelOfferingRow, ContextWindowDisplay, ModelCapabilitySet, CertificationStateBadge, CertificationTimeline, RoutableIndicator, ProbeResultSummary, MetadataSourceIndicator, MetadataConfidenceIndicator, IconButton, Panel } from "../../src/index";

function Card() {

  return (
    <div className="stack">
      <Panel title="Offerings · ops@venom.local">
        <ModelOfferingRow
          identity={<ModelIdentity name="Claude Sonnet 4.5" providerModelId="claude-sonnet-4-5-20250929" />}
          context={<ContextWindowDisplay tokens={1000000} verified source="probe" />}
          capabilities={<ModelCapabilitySet showLabels={false} truths={{ chat: "supported", streaming: "supported", tools: "supported", structured_output: "supported", vision: "supported" }} />}
          certification={<CertificationStateBadge state="certified" />}
          routable={<RoutableIndicator state="certified" truths={{ chat: "supported", tools: "supported" }} required={["chat", "tools"]} />}
          actions={<IconButton icon="flask-conical" label="Run probes" size="sm" />} />
        <ModelOfferingRow
          identity={<ModelIdentity name="GLM-5 Free" providerModelId="glm-5-free" />}
          context={<ContextWindowDisplay tokens={256000} verified={false} source="provider_discovery" />}
          capabilities={<ModelCapabilitySet showLabels={false} truths={{ chat: "supported", streaming: "supported", tools: "unknown", structured_output: "unknown", vision: "unsupported" }} />}
          certification={<CertificationStateBadge state="certified" />}
          routable={<RoutableIndicator state="certified" truths={{ chat: "supported", vision: "unknown" }} required={["chat", "vision"]} />}
          actions={<IconButton icon="flask-conical" label="Run probes" size="sm" />} />
        <ModelOfferingRow
          identity={<ModelIdentity name="Kimi K2.5" providerModelId="kimi-k2.5" availability="catalog_only" />}
          context={<ContextWindowDisplay tokens={null} />}
          capabilities={<ModelCapabilitySet showLabels={false} truths={{ chat: "unknown", streaming: "unknown" }} />}
          certification={<CertificationStateBadge state="suspended" reason="probe_retry_budget_exhausted" />}
          routable={<RoutableIndicator state="suspended" truths={{ chat: "unknown" }} required={["chat"]} />} />
      </Panel>
      <div style={{ display: "flex", gap: 14, alignItems: "center", flexWrap: "wrap" }}>
        <CertificationTimeline state="probing" />
        <CertificationTimeline state="expired" />
      </div>
      <ProbeResultSummary operation="tools" execution="retryable_failure" truth="unknown" note="429 during probe — capability truth unchanged, retry scheduled with backoff" at="13:58:40Z" />
      <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
        <MetadataSourceIndicator source="probe" />
        <MetadataSourceIndicator source="external_registry" />
        <MetadataConfidenceIndicator confidence={0.6} exactMatch={false} stale observedAt="2026-07-20" />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

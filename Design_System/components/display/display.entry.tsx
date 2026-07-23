// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Badge, StatusBadge, Tag, Kbd, Code, CodeBlock, KeyValueList, Timeline, Stepper, Mark } from "../../src/index";

function Card() {

  return (
    <div>
      <div className="row">
        <StatusBadge status="healthy" label="Healthy" />
        <StatusBadge status="degraded" label="Degraded" />
        <StatusBadge status="warning" label="Stale" />
        <StatusBadge status="critical" label="Unavailable" />
        <StatusBadge status="info" label="Probing" />
        <StatusBadge status="unknown" label="Unknown" />
        <StatusBadge status="inactive" label="Stopped" />
        <Badge tone="tier-lite" mono>LITE</Badge>
        <Badge tone="tier-pro" mono>PRO</Badge>
        <Badge tone="tier-max" mono>MAX</Badge>
        <Badge tone="accent" icon="badge-check">Certified</Badge>
      </div>
      <div className="row">
        <Tag icon="filter" onRemove={() => {}}>funding: free</Tag>
        <Tag mono onRemove={() => {}}>provider:claude-code</Tag>
        <Kbd>Ctrl</Kbd><Kbd>K</Kbd>
        <Code>rolling:3600s</Code>
        <Mark name="opencode-zen" /><Mark name="claude-code" /><Mark name="xai" size="sm" />
      </div>
      <div className="cols">
        <div>
          <KeyValueList items={[
            { key: "External ID", value: "acct_9f2e11c4", mono: true },
            { key: "Plan", value: "Max" },
            { key: "Context (verified)", value: "1,000,000 tokens" },
            { key: "Last health check", value: "2m ago" },
          ]} />
          <div style={{ marginTop: 12 }}>
            <Stepper steps={["Authorize", "Validate", "Sync"]} current={1} />
          </div>
        </div>
        <Timeline items={[
          { title: "Certified tools on claude-sonnet-4-5", time: "14:02:11Z", tone: "accent" },
          { title: "Probe retry scheduled (429)", detail: "capability truth unchanged", time: "13:58:40Z", tone: "warning" },
          { title: "Discovery snapshot applied (gen 41)", time: "13:55:02Z" },
        ]} />
      </div>
      <CodeBlock label="Client setup" code={'export ANTHROPIC_BASE_URL="http://127.0.0.1:8081/v1"\nexport ANTHROPIC_MODEL="venom/max"'} />
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

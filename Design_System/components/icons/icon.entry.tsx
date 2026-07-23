// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Icon } from "../../src/index";

function Card() {

  const concepts = ["provider", "account", "model", "certification", "probe", "quota", "cooldown", "routing", "fallback", "trace", "security", "backup", "restore", "health", "latency", "free", "paid", "unknown"];
  return (
    <div>
      <div className="row">
        {concepts.map((c) => (
          <span key={c} style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 4 }}>
            <Icon name={c} size={18} />
            <span className="vn-caption vn-mono-xs">{c}</span>
          </span>
        ))}
      </div>
      <div className="row">
        <Icon name="quota" size={12} /><Icon name="quota" size={16} /><Icon name="quota" size={20} /><Icon name="quota" size={28} />
        <span className="vn-caption">sizes 12/16/20/28 · currentColor · decorative unless label given (then role="img")</span>
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Button, IconButton, ButtonGroup, Link, CopyButton, ThemeSwitcher, DensityToggle } from "../../src/index";

function Card() {

  const [theme, setTheme] = React.useState("venom-dark");
  const [density, setDensity] = React.useState("comfortable");
  return (
    <div>
      <div className="row">
        <ThemeSwitcher value={theme} onChange={setTheme} />
        <DensityToggle value={density} onChange={setDensity} />
      </div>
      <div className="row">
        <Button variant="primary" icon="plus">Connect provider</Button>
        <Button variant="secondary">Refresh</Button>
        <Button variant="ghost">Cancel</Button>
        <Button variant="danger" icon="unplug">Disconnect</Button>
        <Button variant="primary" loading>Validating</Button>
        <Button variant="primary" disabled>Disabled</Button>
      </div>
      <div className="row">
        <Button variant="secondary" size="sm">Small</Button>
        <Button variant="secondary">Medium</Button>
        <Button variant="secondary" size="lg">Large</Button>
        <IconButton icon="refresh-cw" label="Refresh quota" />
        <IconButton icon="ellipsis" label="More actions" variant="secondary" />
        <IconButton icon="trash-2" label="Delete key" variant="danger" />
        <CopyButton value="venom/max" label="Copy model id" />
      </div>
      <div className="row">
        <ButtonGroup label="Time range">
          <Button>24h</Button><Button>7d</Button><Button>30d</Button>
        </ButtonGroup>
        <Link href="#">Route trace req_01J9ZK4T7Q</Link>
        <Link href="#" external>models.dev dataset</Link>
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);

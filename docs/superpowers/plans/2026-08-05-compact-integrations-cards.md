# Compact Enterprise Integrations Cards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the "All Integrations" provider catalog view into a compact, responsive enterprise-grade grid (`3-4` columns) with streamlined cards, status indicators, and disabled "Connected" state buttons when linked.

**Architecture:** Update `ProviderCard.tsx` layout to use a compact header, status indicator row, and action footer. Update `components-core.css` and `fleet.css` to refine `.vn-provider-grid` grid template and card styling tokens.

**Tech Stack:** React 18, TypeScript, CSS Grid & Flexbox, Vitest / Playwright.

## Global Constraints
- ALL code, comments, docstrings, and plan documents MUST strictly use ENGLISH ONLY.
- Preserve test selector contracts (`.vn-provider-card`, `h3`, `Connected` text).
- Ensure `npm run typecheck` and `npm test` pass cleanly.

---

### Task 1: Redesign `ProviderCard.tsx` Structure

**Files:**
- Modify: `dashboard/src/fleet/ProviderCard.tsx`

**Interfaces:**
- Consumes: `Provider` interface, `accountCount`, `onConnect` callback
- Produces: Compact enterprise integration card with logo, title, auth mode badge, connection status indicator, and action button.

- [ ] **Step 1: Update `ProviderCard.tsx` JSX Structure**

```tsx
import { Badge, Button } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import type { Provider } from "../api/controlClient";
import ProviderLogo from "./ProviderLogo";
import { cardBadgeLabel, providerDisplayName, providerMeta } from "./providerMeta";

export interface ProviderCardProps {
  provider: Provider;
  accountCount: number;
  onConnect: () => void;
}

export default function ProviderCard(props: ProviderCardProps) {
  const { provider, accountCount, onConnect } = props;

  const connected = accountCount > 0;
  const setupRequired = !provider.configured;
  const connectable = provider.auth_mode === "api_key" || provider.auth_mode === "oauth2";
  const name = providerDisplayName(provider);
  const meta = providerMeta(provider.id);

  return (
    <article
      className={[
        "vn-card vn-provider-card",
        connected ? "vn-provider-card--connected" : "",
      ].filter(Boolean).join(" ")}
    >
      <div className="vn-provider-card-head">
        <div className="vn-provider-card-identity">
          <ProviderLogo slug={provider.id} name={name} size="md" />
          <div className="vn-provider-card-title">
            <div className="vnd-card-name-row">
              <h3 role="heading" aria-level={2}>{name}</h3>
              {meta ? (
                <a
                  className="vnd-icon-link"
                  href={meta.siteUrl}
                  target="_blank"
                  rel="noreferrer noopener"
                  aria-label={`Open ${meta.siteLabel} in a new tab`}
                  title={`Open ${meta.siteLabel} in a new tab`}
                >
                  <Icon name="external-link" size={12} />
                </a>
              ) : null}
            </div>
            <div className="vn-provider-card-status-row">
              {connected ? (
                <span className="vn-provider-card-linked">
                  <span className="vnd-status-dot vnd-status-dot--healthy" />
                  Connected ({accountCount})
                </span>
              ) : (
                <span className="vn-provider-card-linked vn-provider-card-linked--idle">
                  No connections
                </span>
              )}
            </div>
          </div>
        </div>
        <div className="vn-provider-card-meta">
          <Badge tone="inactive" mono outline title={"auth_mode: " + provider.auth_mode}>
            {cardBadgeLabel(provider)}
          </Badge>
        </div>
      </div>

      <div className="vn-provider-card-actions">
        {connected ? (
          <Button variant="secondary" icon="circle-check" disabled className="w-full justify-center text-status-healthy-fg">
            Connected
          </Button>
        ) : setupRequired ? (
          <Button variant="secondary" icon="plug" disabled className="w-full justify-center text-status-warning-fg">
            Setup required
          </Button>
        ) : !connectable ? (
          <Button variant="secondary" icon="plug" disabled className="w-full justify-center text-status-healthy-fg">
            Integration unavailable
          </Button>
        ) : (
          <Button variant="secondary" icon="plug" onClick={onConnect} className="w-full justify-center">
            {meta?.cta ?? "Connect Integration"}
          </Button>
        )}
      </div>
    </article>
  );
}
```

- [ ] **Step 2: Commit Task 1 Changes**

```bash
git add dashboard/src/fleet/ProviderCard.tsx
git commit -m "feat(fleet): redesign ProviderCard into compact enterprise layout"
```

---

### Task 2: Update CSS Grid and Card Styles

**Files:**
- Modify: `Design_System/css/components-core.css:740-763`
- Modify: `dashboard/src/fleet/fleet.css`

**Interfaces:**
- Consumes: CSS Grid container properties
- Produces: `.vn-provider-grid` multi-column layout (`repeat(auto-fill, minmax(280px, 1fr))`), compact card padding, dot status styling.

- [ ] **Step 1: Update `.vn-provider-grid` and `.vn-provider-card` in `components-core.css`**

In `Design_System/css/components-core.css`:
```css
.vn-provider-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--space-3);
  min-width: 0;
}

.vn-provider-card {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  min-width: 0;
  gap: var(--space-3);
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition: transform var(--duration-fast) var(--easing-standard), border-color var(--duration-fast) var(--easing-standard), box-shadow var(--duration-fast) var(--easing-standard);
}

.vn-provider-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
}

.vn-provider-card-identity {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
  flex: 1;
}

.vn-provider-card-title h3 {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--text-primary);
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-semibold);
  line-height: var(--font-line-height-snug);
}

.vn-provider-card-status-row {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-top: 2px;
}

.vn-provider-card-linked {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--status-healthy-fg);
  font-size: var(--font-size-xs);
  font-weight: var(--font-weight-medium);
}

.vn-provider-card-linked--idle {
  color: var(--text-muted);
  font-weight: var(--font-weight-normal);
}

.vnd-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--status-healthy-fg);
  display: inline-block;
}

.vn-provider-card-actions {
  padding-top: var(--space-2);
  border-top: var(--border-hairline) solid var(--border-default);
}
```

- [ ] **Step 2: Commit CSS Changes**

```bash
git add Design_System/css/components-core.css dashboard/src/fleet/fleet.css
git commit -m "feat(ds,fleet): update provider grid to compact multi-column responsive layout"
```

---

### Task 3: Build & Verify Tests Across Design System & Dashboard

**Files:**
- Test: `dashboard/tests/` and `dashboard/src/fleet/FleetOverview.test.tsx`

- [ ] **Step 1: Rebuild Design System**

Run: `cd Design_System && npm run build`

- [ ] **Step 2: Run Dashboard Typecheck & Unit Tests**

Run: `npm run typecheck` and `npm test` inside `dashboard/`
Expected: 0 errors, all 502 tests pass.

- [ ] **Step 3: Commit Build & Verification**

```bash
git add Design_System/dist dashboard/
git commit -m "test(fleet): verify compact provider card grid functionality"
```

# Design Specification: Compact Enterprise Integrations Cards & Grid

## Overview
This specification details the redesign of the "All Integrations" catalog view in the Providers page (`dashboard/src/fleet/`). The legacy giant provider cards will be transformed into a sleek, compact, enterprise-grade card grid (`3-4` columns on desktop), improving scannability while preserving all functionality including logo displays, auth mode badges, status indicators, and disabled/active connection buttons.

## Visual & Component Architecture

### 1. Card Container & Grid Layout (`.vn-provider-grid` & `.vn-provider-card`)
- **Grid Layout**: Updated `.vn-provider-grid` to responsive grid layout: `grid-template-columns: repeat(auto-fill, minmax(280px, 1fr))` (3-4 cards per row on desktop).
- **Compact Card Padding & Styling**: Reduced padding from `var(--space-5)` (24px) to `var(--space-4)` (16px), gap `var(--space-3)`.
- **Dual Theme Support**:
  - `venom-dark`: Dark sunken glass background (`rgba(18, 22, 31, 0.75)`), hairline border (`--border-default`), elevation shadow on hover.
  - `venom-light`: Clean white backdrop (`#ffffff`), subtle gray hairline border, soft drop shadow.

### 2. Card Header & Identity (`ProviderCard.tsx`)
- **Logo & Title Row**:
  - Left: Provider logo (`36px x 36px` size).
  - Center: Provider title `h3` (`font-size: 0.95rem`, `font-weight: 600`, line height snug) + optional external site link.
  - Right / Top-Right Badges: Muted auth mode badge (`OAUTH` / `API KEY`).
- **Status Indicator Row**:
  - Connected: Green health dot + text `"Connected (`N` account(s))"`.
  - Unconnected: Muted text `"No connections"`.
  - Setup Required: Amber setup badge `"Setup required"`.

### 3. Action Area (`ProviderCard.tsx`)
- Full-width or right-aligned action button:
  - If connected: Disabled button `<Button variant="secondary" icon="circle-check" disabled>` displaying `"Connected"` (text color `--status-healthy-fg`).
  - If setup required: Disabled button displaying `"Setup required"` (text color `--status-warning-fg`).
  - If connectable & unconnected: Secondary/Primary action button displaying `"Connect Integration"` (or CTA string) with plug icon.

## Verification & Compatibility
- Ensure all existing Playwright / Vitest tests in `FleetOverview.test.tsx` pass without breaking selector contracts (e.g. `.vn-provider-card`, `h3`, `getByText("Connected")`).
- Run `npm run typecheck` and `npm test` in `dashboard/`.

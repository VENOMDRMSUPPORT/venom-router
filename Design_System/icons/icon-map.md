# Venom icon language

One set: **Lucide**, pinned `lucide-static@0.453.0` (24px grid, 2px stroke, round joins). Icons render through `.vn-icon .vn-icon--<glyph>` (CSS mask + `currentColor`) or the `Icon` React component (`components/icons/Icon.tsx`), which also accepts the **domain concept names** below. Icons inherit text color in every theme and are sized by tokens (12/16/20/28).

**Rules**
- An icon never carries meaning alone. Critical state = icon + label (+ tooltip/detail where needed).
- Decorative icons: `aria-hidden="true"`. Meaning-bearing icons standing alone: `role="img"` + `aria-label`.
- Capability icons may take controlled accent tints (status/tier tokens) but must stay legible in all three themes and never rely on color alone.
- Never hand-draw replacement glyphs; extend this map from the same pinned set.
- Offline packaging: the pinned SVGs are vendored into `assets/icons/` and `icons/icons.css` references them locally — no runtime network access. Run `npm run vendor:icons` after adding a new glyph reference (copies from the pinned `lucide-static` devDependency and rewrites the CSS).

## Domain icon map (canonical)

| Concept | Glyph | Concept | Glyph |
|---|---|---|---|
| chat | `message-square` | cooldown | `hourglass` |
| streaming | `radio` | routing | `route` |
| tools | `wrench` | fallback | `corner-down-right` |
| structured output | `braces` | diagnostics | `activity` |
| reasoning | `brain` | trace | `list-tree` |
| vision input | `eye` | security | `lock` |
| context window | `scan-text` | backup | `archive` |
| coding | `code` | restore | `archive-restore` |
| authentication | `shield-check` | health | `heart-pulse` |
| API key | `key-round` | latency | `timer` |
| OAuth | `fingerprint` | cost | `coins` |
| provider | `server` | free | `hand-coins` |
| account | `user-round` | paid | `credit-card` |
| model | `box` | unknown | `circle-help` |
| certification | `badge-check` | probe | `flask-conical` |

## Supporting UI glyphs

Navigation: `layout-dashboard` `server` `box` `route` `terminal` `chart-line` `gauge` `heart-pulse` `activity` `key-round` `settings`.
Feedback/state: `check` `x` `circle-check` `circle-x` `circle-alert` `triangle-alert` `info` `circle-help` `loader-circle` `ban` `pause` `play` `zap`.
Chrome/controls: `chevron-down` `chevron-right` `chevron-left` `chevrons-up-down` `search` `copy` `eye` `eye-off` `plus` `minus` `ellipsis` `external-link` `refresh-cw` `rotate-ccw` `trash-2` `filter` `arrow-right` `clock` `database` `download` `upload` `log-out` `inbox` `sliders-horizontal` `moon` `sun` `contrast` `rows-3` `plug` `unplug` `power` `git-branch` `shield`.

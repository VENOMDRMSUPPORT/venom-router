# Keyboard walkthrough checklist (manual verification)

Run in both themes; confirm the focus ring is visible at every stop.

**Global**
- [ ] Tab order follows visual order on every surface; no keyboard trap outside dialogs.
- [ ] `:focus-visible` ring appears on every interactive element (buttons, links, inputs, rows, tabs, menu items).

**Controls**
- [ ] Button/IconButton: Enter + Space activate; disabled unreachable side effects.
- [ ] Select: native keyboard model. Combobox: ArrowDown opens/moves, ArrowUp moves, Enter selects, Escape closes, typing filters.
- [ ] Checkbox/Radio/Switch: Space toggles; radio arrows move within group.
- [ ] Slider: arrows adjust; value readout updates.

**Composite**
- [ ] Tabs: Left/Right/Home/End move + select; panel focus follows.
- [ ] DropdownMenu/ContextMenu: Arrows move active item, Enter selects, Escape closes and returns focus to trigger.
- [ ] Dialog/AlertDialog/Drawer: focus moves in on open, Tab wraps, Escape closes (except AlertDialog), focus restores to opener.
- [ ] DataTable: sortable headers focusable (Enter toggles, aria-sort announced); clickable rows Enter/Space open the Drawer.
- [ ] Tooltip: appears on keyboard focus, Escape dismisses.
- [ ] Accordion: Enter/Space toggles; aria-expanded announced.

**Domain**
- [ ] SecretRevealControl: reveal/hide reachable; blocked state announces re-verification requirement; revealed value is removed from the DOM on hide.
- [ ] ReverificationPrompt: focus lands in password field; Escape cancels without side effects.
- [ ] ReconciliationStatus actions (Re-sync / Accept estimate) reachable and labeled.
- [ ] RouteDecisionTrace: header + rows navigable; exclusion codes readable as text.

Responsive modal work surface. It uses the Drawer's focus trap, Escape handling, scrim dismissal, and focus restoration. At 900px and above it is a right drawer; below 900px it is a bottom sheet.

```tsx
<AdaptiveSheet open={open} onClose={() => setOpen(false)} title="Create item">…</AdaptiveSheet>
```

Global page-context row. Put the breadcrumb or page identity in `leading`, the primary page command in `actions`, and optional filters or counters in `secondary`. The component owns responsive alignment only; action state remains with the page shell.

```tsx
<PageContextBar leading={<Breadcrumbs items={items} />} actions={<Button>New item</Button>} />
```

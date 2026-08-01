One data source rendered as a table from 900px upward and cards below 900px. Columns and cards must expose the same facts; do not add a view preference or duplicate fetching state.

```tsx
<ResponsiveCollection rows={rows} columns={columns} rowKey="id" label="Items" renderCard={(row) => <Card>{row.name}</Card>} />
```

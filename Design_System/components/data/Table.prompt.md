Dense operational table with sort/loading/empty/selection built in.

```jsx
<DataTable label="Offerings" columns={[{key:"model",label:"Model",mono:true},{key:"ctx",label:"Context",numeric:true}]} rows={rows} onRowClick={openDrawer} empty={<EmptyState title="No offerings" />} />
```

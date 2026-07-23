Controlled density picker for exactly `comfortable` / `compact`. Apply it to the root the same way as theme.

```jsx
const [density, setDensity] = React.useState("comfortable");
React.useEffect(() => { document.documentElement.setAttribute("data-density", density); }, [density]);

<DensityToggle value={density} onChange={setDensity} />
```

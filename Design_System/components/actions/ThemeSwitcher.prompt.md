Controlled theme picker for exactly `venom-dark` / `venom-light` / `venom-hc`. The app owns the source of truth (state + persistence, e.g. `PUT /settings`); apply it to the root via `document.documentElement.setAttribute("data-theme", value)` — or `applyTheme` from the `themes` entry point — in the same effect that calls `onChange`.

```jsx
const [theme, setTheme] = React.useState("venom-dark");
React.useEffect(() => { document.documentElement.setAttribute("data-theme", theme); }, [theme]);

<ThemeSwitcher value={theme} onChange={setTheme} />
```

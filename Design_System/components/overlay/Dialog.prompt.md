Modal with focus trap/restore; AlertDialog for blocking confirmations (always non-dismissible, no scrim/Escape close). Both participate in the shared overlay stack (`overlay-stack.ts`) so a Dialog opened from inside a Drawer takes over Escape/Tab correctly while it is topmost.

```jsx
<Dialog open={open} onClose={close} title="Connect OpenCode Zen" footer={<><Button onClick={close}>Cancel</Button><Button variant="primary">Connect</Button></>}>...</Dialog>
```

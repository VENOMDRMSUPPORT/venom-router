Right-hand inspector for row detail. Focus moves in on open (first focusable, or `initialFocusRef` when a specific control should get it), Tab/Shift+Tab wraps inside the drawer, Escape closes and focus returns to the exact element that opened it. `dismissible={false}` removes the close button and disables scrim/Escape dismissal for a forced-choice flow. `Sheet` is a plain alias — same component, same behavior.

```jsx
<Drawer open={!!row} onClose={close} title={row?.model} wide>...</Drawer>

// send initial focus to a specific control instead of the first focusable element
const nameRef = React.useRef(null);
<Drawer open={open} onClose={close} title="Edit account" initialFocusRef={nameRef}>
  <Input ref={nameRef} label="Name" />
</Drawer>
```

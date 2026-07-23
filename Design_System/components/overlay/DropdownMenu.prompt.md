Overflow/action menu; danger items use destructive tokens. Real DOM focus moves between items (ArrowDown/ArrowUp/Home/End, roving tabindex) — disabled items are skipped by every navigation path. Enter/Space activate via native button semantics. Escape closes and returns focus to the trigger; Tab closes and continues to the next widget. Single-character typeahead jumps to the next item whose label starts with that letter (no type-ahead buffering — repeat the letter to cycle).

```jsx
<DropdownMenu trigger={<IconButton icon="ellipsis" label="Account actions" />} items={[{label:"Refresh", icon:"refresh-cw"},{type:"separator"},{label:"Disconnect", icon:"unplug", danger:true}]} />
```

Tablet/mobile section navigation for an application shell. Pass four stable sections, the active destination key, and one navigation callback. A one-item section navigates directly; multi-item sections open an accessible tray that closes after selection, outside press, or Escape.

```tsx
<SectionDeck sections={sections} activeKey={activeKey} onNavigate={setActiveKey} />
```

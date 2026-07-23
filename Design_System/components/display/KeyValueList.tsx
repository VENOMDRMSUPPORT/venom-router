import * as React from "react";

export interface KeyValueItem {
  key: React.ReactNode;
  value: React.ReactNode;
  mono?: boolean;
}

export interface KeyValueListProps {
  items?: KeyValueItem[];
  className?: string;
}

export function KeyValueList(props: KeyValueListProps) {
  const { items = [], className = "" } = props;
  return (
    <dl className={("vn-kv " + className).trim()}>
      {items.map((it, i) => (
        <React.Fragment key={i}>
          <dt>{it.key}</dt>
          <dd className={it.mono ? "vn-mono" : undefined}>{it.value}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}

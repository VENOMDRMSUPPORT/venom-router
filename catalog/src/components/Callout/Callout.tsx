import { LuInfo } from "react-icons/lu";
import styles from './Callout.module.css';

interface CalloutProps {
  children: React.ReactNode;
}

/** Subtle information callout — Vercel-style, no loud colors. */
export function Callout({ children }: CalloutProps) {
  return (
    <div className={styles.callout}>
      <LuInfo size={15} className={styles.icon} />
      <div className={styles.content}>{children}</div>
    </div>
  );
}

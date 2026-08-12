import { LuMenu } from "react-icons/lu";
import styles from './MobileMenu.module.css';

interface MobileMenuProps {
  open: boolean;
  onClick: () => void;
}

export function MobileMenu({ open, onClick }: MobileMenuProps) {
  if (open) return null;

  return (
    <button
      className={styles.btn}
      onClick={onClick}
      aria-label="Toggle navigation"
    >
      <LuMenu size={18} />
    </button>
  );
}

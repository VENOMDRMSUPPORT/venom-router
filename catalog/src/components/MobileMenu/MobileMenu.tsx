import { LuMenu } from "react-icons/lu";
import styles from './MobileMenu.module.css';

interface MobileMenuProps {
  onClick: () => void;
}

export function MobileMenu({ onClick }: MobileMenuProps) {
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

import { LuSun, LuMoon } from 'react-icons/lu';
import type { Theme } from '../../hooks/useTheme';
import styles from './ThemeToggle.module.css';

interface ThemeToggleProps {
  theme: Theme;
  onToggle: () => void;
}

export function ThemeToggle({ theme, onToggle }: ThemeToggleProps) {
  const isDark = theme === 'dark';
  return (
    <button
      className={styles.toggle}
      onClick={onToggle}
      aria-label={`Switch to ${isDark ? 'light' : 'dark'} theme`}
      title={`Switch to ${isDark ? 'light' : 'dark'} theme`}
    >
      {isDark ? <LuSun size={16} /> : <LuMoon size={16} />}
    </button>
  );
}

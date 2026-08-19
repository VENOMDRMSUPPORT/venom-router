import { useCallback, useEffect, useState } from 'react';

const STORAGE_KEY = 'catalog-theme';

export type Theme = 'dark' | 'light';

/**
 * Theme toggle with localStorage persistence.
 * Defaults to dark (Vercel's signature look).
 */
export function useTheme(): {
  theme: Theme;
  toggleTheme: () => void;
} {
  const [theme, setTheme] = useState<Theme>(() => {
    if (typeof window === 'undefined') return 'dark';
    return (localStorage.getItem(STORAGE_KEY) as Theme) || 'dark';
  });

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    localStorage.setItem(STORAGE_KEY, theme);
  }, [theme]);

  useEffect(() => {
    const handleThemeChange = (event: Event) => {
      const next = (event as CustomEvent<Theme>).detail;
      if (next === 'dark' || next === 'light') setTheme(next);
    };

    window.addEventListener('catalog-theme-change', handleThemeChange);
    return () => window.removeEventListener('catalog-theme-change', handleThemeChange);
  }, []);
  const toggleTheme = useCallback(() => {
    setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'));
  }, []);

  return { theme, toggleTheme };
}

import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

export const THEME_STORAGE_KEY = 'catalog-theme';
export const SETTINGS_STORAGE_KEY = 'venom-catalog-settings';

export type Theme = 'dark' | 'light';

type StoredSettings = {
  theme?: Theme;
  defaultView?: 'grid' | 'table';
  reduceMotion?: boolean;
};

type ThemeContextValue = {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
  reduceMotion: boolean;
  setReduceMotion: (reduceMotion: boolean) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function isTheme(value: unknown): value is Theme {
  return value === 'dark' || value === 'light';
}

function readStoredSettings(raw?: string | null): StoredSettings {
  if (typeof window === 'undefined') return {};

  try {
    const value = raw === undefined ? window.localStorage.getItem(SETTINGS_STORAGE_KEY) : raw;
    if (!value) return {};
    const parsed = JSON.parse(value) as Record<string, unknown>;
    return {
      theme: isTheme(parsed.theme) ? parsed.theme : undefined,
      defaultView: parsed.defaultView === 'grid' ? 'grid' : parsed.defaultView === 'table' ? 'table' : undefined,
      reduceMotion: parsed.reduceMotion === true,
    };
  } catch {
    return {};
  }
}

function readStoredTheme(): Theme {
  if (typeof window === 'undefined') return 'dark';

  try {
    const direct = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (direct !== null) return isTheme(direct) ? direct : 'dark';
    return readStoredSettings().theme ?? 'dark';
  } catch {
    return 'dark';
  }
}

function readStoredReduceMotion(): boolean {
  return readStoredSettings().reduceMotion === true;
}

function readSystemReduceMotion(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  try {
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

function persistSettings(patch: StoredSettings): void {
  if (typeof window === 'undefined') return;

  try {
    const current = readStoredSettings();
    window.localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify({ ...current, ...patch }));
  } catch {
    // Browser privacy mode, quota limits, and denied storage are non-fatal.
  }
}

export function saveCatalogSettings(patch: StoredSettings): void {
  persistSettings(patch);
}

function applyThemeToDocument(theme: Theme, reduceMotion: boolean, systemReduceMotion: boolean): void {
  if (typeof document === 'undefined') return;

  const root = document.documentElement;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
  root.dataset.reduceMotion = reduceMotion || systemReduceMotion ? 'true' : 'false';
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [themeState, setThemeState] = useState<Theme>(readStoredTheme);
  const [reduceMotion, setReduceMotionState] = useState(readStoredReduceMotion);
  const [systemReduceMotion, setSystemReduceMotion] = useState(readSystemReduceMotion);
  const themeRef = useRef(themeState);
  const reduceMotionRef = useRef(reduceMotion);
  const systemReduceMotionRef = useRef(systemReduceMotion);
  const transitionFramesRef = useRef(new Set<number>());
  const transitionGenerationRef = useRef(0);

  const applyCurrentTheme = useCallback((nextTheme: Theme, nextReduceMotion: boolean, nextSystemReduceMotion: boolean) => {
    if (typeof document === 'undefined') return;

    const root = document.documentElement;
    const generation = transitionGenerationRef.current + 1;
    transitionGenerationRef.current = generation;
    if (typeof window.cancelAnimationFrame === 'function') {
      for (const frame of transitionFramesRef.current) window.cancelAnimationFrame(frame);
    }
    transitionFramesRef.current.clear();

    root.dataset.themeChanging = 'true';
    applyThemeToDocument(nextTheme, nextReduceMotion, nextSystemReduceMotion);

    if (nextReduceMotion || nextSystemReduceMotion || typeof window.requestAnimationFrame !== 'function') {
      delete root.dataset.themeChanging;
      return;
    }

    const scheduleFrame = (callback: FrameRequestCallback) => {
      const frame = window.requestAnimationFrame((timestamp) => {
        transitionFramesRef.current.delete(frame);
        callback(timestamp);
      });
      transitionFramesRef.current.add(frame);
    };
    scheduleFrame(() => scheduleFrame(() => {
      if (generation === transitionGenerationRef.current) delete root.dataset.themeChanging;
    }));
  }, []);

  const setTheme = useCallback((nextTheme: Theme) => {
    if (!isTheme(nextTheme)) return;
    themeRef.current = nextTheme;
    applyCurrentTheme(nextTheme, reduceMotionRef.current, systemReduceMotionRef.current);
    setThemeState(nextTheme);

    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, nextTheme);
    } catch {
      // Persistence is best effort; the in-memory and DOM state still update.
    }
    persistSettings({ theme: nextTheme });
  }, [applyCurrentTheme]);

  const setReduceMotion = useCallback((nextReduceMotion: boolean) => {
    const normalized = nextReduceMotion === true;
    reduceMotionRef.current = normalized;
    applyCurrentTheme(themeRef.current, normalized, systemReduceMotionRef.current);
    setReduceMotionState(normalized);
    persistSettings({ reduceMotion: normalized, theme: themeRef.current });
  }, [applyCurrentTheme]);

  const toggleTheme = useCallback(() => {
    setTheme(themeRef.current === 'dark' ? 'light' : 'dark');
  }, [setTheme]);

  useEffect(() => {
    applyCurrentTheme(themeRef.current, reduceMotionRef.current, systemReduceMotionRef.current);

    const handleStorage = (event: StorageEvent) => {
      if (event.key === THEME_STORAGE_KEY) {
        const nextTheme = isTheme(event.newValue) ? event.newValue : 'dark';
        if (nextTheme !== themeRef.current) {
          themeRef.current = nextTheme;
          applyCurrentTheme(nextTheme, reduceMotionRef.current, systemReduceMotionRef.current);
          setThemeState(nextTheme);
        }
      }

      if (event.key === SETTINGS_STORAGE_KEY) {
        const nextReduceMotion = readStoredSettings(event.newValue).reduceMotion === true;
        if (nextReduceMotion !== reduceMotionRef.current) {
          reduceMotionRef.current = nextReduceMotion;
          applyCurrentTheme(themeRef.current, nextReduceMotion, systemReduceMotionRef.current);
          setReduceMotionState(nextReduceMotion);
        }
      }
    };

    window.addEventListener('storage', handleStorage);
    return () => window.removeEventListener('storage', handleStorage);
  }, [applyCurrentTheme]);

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;

    let mediaQuery: MediaQueryList;
    try {
      mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
    } catch {
      return;
    }

    const handlePreferenceChange = (event: MediaQueryListEvent) => {
      systemReduceMotionRef.current = event.matches;
      setSystemReduceMotion(event.matches);
      applyCurrentTheme(themeRef.current, reduceMotionRef.current, event.matches);
    };

    mediaQuery.addEventListener?.('change', handlePreferenceChange);
    return () => mediaQuery.removeEventListener?.('change', handlePreferenceChange);
  }, [applyCurrentTheme]);

  useEffect(() => () => {
    transitionGenerationRef.current += 1;
    if (typeof window.cancelAnimationFrame === 'function') {
      for (const frame of transitionFramesRef.current) window.cancelAnimationFrame(frame);
    }
    transitionFramesRef.current.clear();
    delete document.documentElement.dataset.themeChanging;
  }, []);

  const value = useMemo<ThemeContextValue>(() => ({
    theme: themeState,
    setTheme,
    toggleTheme,
    reduceMotion,
    setReduceMotion,
  }), [reduceMotion, setReduceMotion, setTheme, themeState, toggleTheme]);

  return createElement(ThemeContext.Provider, { value }, children);
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (!context) throw new Error('useTheme must be used within ThemeProvider.');
  return context;
}

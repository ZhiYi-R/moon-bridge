import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState
} from "react";
import {
  applyThemeTokens,
  CONSOLE_THEMES,
  DEFAULT_CONSOLE_THEME,
  migrateStoredTheme,
  type ConsoleTheme
} from "./tokens";

export const CONSOLE_THEME_STORAGE_KEY = "moonbridge.console.theme";

type ConsoleThemeContextValue = {
  theme: ConsoleTheme;
  themes: readonly ConsoleTheme[];
  setTheme: (theme: ConsoleTheme) => void;
  /** Cycles to the next theme pack (for compact controls / tests). */
  toggleTheme: () => void;
};

const ConsoleThemeContext = createContext<ConsoleThemeContextValue | undefined>(
  undefined
);

function readStoredTheme(): ConsoleTheme {
  if (typeof window === "undefined") {
    return DEFAULT_CONSOLE_THEME;
  }

  try {
    if (!window.localStorage) {
      return DEFAULT_CONSOLE_THEME;
    }
    return migrateStoredTheme(window.localStorage.getItem(CONSOLE_THEME_STORAGE_KEY));
  } catch {
    return DEFAULT_CONSOLE_THEME;
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ConsoleTheme>(readStoredTheme);

  const setTheme = useCallback((nextTheme: ConsoleTheme) => {
    setThemeState(nextTheme);
  }, []);

  const toggleTheme = useCallback(() => {
    setThemeState((current) => {
      const index = CONSOLE_THEMES.indexOf(current);
      const nextIndex = index < 0 ? 0 : (index + 1) % CONSOLE_THEMES.length;
      return CONSOLE_THEMES[nextIndex]!;
    });
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = theme;
    applyThemeTokens(theme, root);
    try {
      window.localStorage?.setItem(CONSOLE_THEME_STORAGE_KEY, theme);
    } catch {
      // Storage may be disabled in hardened browser contexts.
    }
  }, [theme]);

  const value = useMemo(
    () => ({ theme, themes: CONSOLE_THEMES, setTheme, toggleTheme }),
    [theme, setTheme, toggleTheme]
  );

  return (
    <ConsoleThemeContext.Provider value={value}>
      {children}
    </ConsoleThemeContext.Provider>
  );
}

export function useConsoleTheme(): ConsoleThemeContextValue {
  const context = useContext(ConsoleThemeContext);
  if (!context) {
    throw new Error("useConsoleTheme must be used within ThemeProvider");
  }
  return context;
}

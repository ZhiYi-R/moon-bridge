export type ConsoleTheme =
  | "bauhaus-classic"
  | "bauhaus-dark"
  | "bauhaus-weimar"
  | "bauhaus-mono";

export const CONSOLE_THEMES: readonly ConsoleTheme[] = [
  "bauhaus-classic",
  "bauhaus-dark",
  "bauhaus-weimar",
  "bauhaus-mono"
] as const;

export const DEFAULT_CONSOLE_THEME: ConsoleTheme = "bauhaus-dark";

export type ThemeTokens = Record<string, string>;

/** Shared Bauhaus geometry / type / motion — applied with every color pack. */
export const bauhausBaseTokens: ThemeTokens = {
  // Shape — hard edges
  "--mb-shape-xs": "0px",
  "--mb-shape-sm": "0px",
  "--mb-shape-md": "0px",
  "--mb-shape-lg": "0px",
  "--mb-shape-xl": "0px",
  "--mb-shape-2xl": "0px",
  "--mb-shape-full": "0px",
  "--mb-button-shape": "0px",
  "--mb-shape-panel": "0px",
  "--mb-border-width": "2px",
  "--mb-border-width-thick": "3px",

  // Type
  "--mb-font-ui":
    '"Space Grotesk", "Helvetica Neue", Helvetica, Arial, ui-sans-serif, system-ui, sans-serif',
  "--mb-font-mono":
    '"IBM Plex Mono", "JetBrains Mono", "SFMono-Regular", Consolas, monospace',
  "--mb-type-display":
    '700 clamp(1.85rem, 1.35rem + 1.8vw, 2.6rem)/1.05 "Space Grotesk", Helvetica, Arial, sans-serif',
  "--mb-tracking-display": "0.02em",
  "--mb-type-title": "700 1.25rem/1.2 var(--mb-font-ui)",
  "--mb-type-body": "500 0.9375rem/1.45 var(--mb-font-ui)",
  "--mb-type-label": "600 0.75rem/1.2 var(--mb-font-ui)",
  "--mb-type-micro": "600 0.6875rem/1.15 var(--mb-font-ui)",

  // Layout
  "--mb-content-max": "1560px",
  "--mb-content-gutter": "clamp(16px, 3.2vw, 44px)",
  "--mb-nav-width": "200px",
  "--mb-header-height": "72px",

  // Motion — short, linear-ish
  "--mb-ease-standard": "cubic-bezier(0.25, 0.1, 0.25, 1)",
  "--mb-ease-decelerate": "cubic-bezier(0, 0, 0.2, 1)",
  "--mb-ease-accelerate": "cubic-bezier(0.4, 0, 1, 1)",
  "--mb-ease-spring": "cubic-bezier(0.25, 0.1, 0.25, 1)",
  "--mb-duration-short": "100ms",
  "--mb-duration-medium": "160ms",
  "--mb-duration-long": "240ms",
  "--mb-motion-standard": "var(--mb-duration-medium) var(--mb-ease-standard)",
  "--mb-motion-emphasized": "var(--mb-duration-long) var(--mb-ease-decelerate)",

  // State layers
  "--mb-state-hover": "0.08",
  "--mb-state-focus": "0.12",
  "--mb-state-press": "0.16",

  // Hard elevation (offset blocks, not soft shadows)
  "--mb-elevation-1": "3px 3px 0 color-mix(in srgb, var(--mb-color-shadow) 55%, transparent)",
  "--mb-elevation-2": "4px 4px 0 color-mix(in srgb, var(--mb-color-shadow) 65%, transparent)",
  "--mb-elevation-3": "6px 6px 0 color-mix(in srgb, var(--mb-color-shadow) 75%, transparent)",
  "--mb-elevation-4": "8px 8px 0 color-mix(in srgb, var(--mb-color-shadow) 80%, transparent)",
  "--mb-elevation-5": "10px 10px 0 color-mix(in srgb, var(--mb-color-shadow) 85%, transparent)"
};

/**
 * Bauhaus color packs. Roles mirror the previous console token names so existing
 * class-based styles can be restyled without renaming every property.
 */
export const themeTokens: Record<ConsoleTheme, ThemeTokens> = {
  "bauhaus-classic": {
    "--mb-color-primary": "#E30613",
    "--mb-color-on-primary": "#ffffff",
    "--mb-color-primary-container": "#ffd6d8",
    "--mb-color-on-primary-container": "#5c0006",
    "--mb-color-primary-fixed": "#ffd6d8",
    "--mb-color-primary-fixed-dim": "#ffb3b8",
    "--mb-color-secondary": "#0057B8",
    "--mb-color-on-secondary": "#ffffff",
    "--mb-color-secondary-container": "#d4e4ff",
    "--mb-color-on-secondary-container": "#002a5c",
    "--mb-color-tertiary": "#E6B800",
    "--mb-color-on-tertiary": "#1a1400",
    "--mb-color-tertiary-container": "#fff0a8",
    "--mb-color-on-tertiary-container": "#3d3000",
    "--mb-color-warning": "#C99700",
    "--mb-color-on-warning": "#1a1400",
    "--mb-color-warning-container": "#fff0a8",
    "--mb-color-on-warning-container": "#3d3000",
    "--mb-color-success": "#1B7A3D",
    "--mb-color-on-success": "#ffffff",
    "--mb-color-success-container": "#c8f0d4",
    "--mb-color-on-success-container": "#003d18",
    "--mb-color-error": "#E30613",
    "--mb-color-on-error": "#ffffff",
    "--mb-color-error-container": "#ffd6d8",
    "--mb-color-on-error-container": "#5c0006",
    "--mb-color-surface": "#f4f2ee",
    "--mb-color-surface-dim": "#e4e1db",
    "--mb-color-surface-bright": "#ffffff",
    "--mb-color-surface-container-lowest": "#ffffff",
    "--mb-color-surface-container-low": "#f0eee9",
    "--mb-color-surface-container": "#eae7e1",
    "--mb-color-surface-container-high": "#e2ded6",
    "--mb-color-surface-container-highest": "#d8d3ca",
    "--mb-color-on-surface": "#111111",
    "--mb-color-on-surface-variant": "#3a3a3a",
    "--mb-color-outline": "#111111",
    "--mb-color-outline-variant": "#9a958c",
    "--mb-color-shadow": "#111111",
    "--mb-color-scrim": "#000000",
    "--mb-color-inverse-surface": "#111111",
    "--mb-color-inverse-on-surface": "#f4f2ee",
    "--mb-color-inverse-primary": "#ff8a90",
    "--mb-color-grid": "#111111",
    "--mb-color-accent-red": "#E30613",
    "--mb-color-accent-blue": "#0057B8",
    "--mb-color-accent-yellow": "#FFD100",
    "--mb-color-scheme": "light"
  },
  "bauhaus-dark": {
    "--mb-color-primary": "#FF3B45",
    "--mb-color-on-primary": "#1a0002",
    "--mb-color-primary-container": "#7a0a12",
    "--mb-color-on-primary-container": "#ffd6d8",
    "--mb-color-primary-fixed": "#ffd6d8",
    "--mb-color-primary-fixed-dim": "#ff8a90",
    "--mb-color-secondary": "#4D9FFF",
    "--mb-color-on-secondary": "#001833",
    "--mb-color-secondary-container": "#0a3a73",
    "--mb-color-on-secondary-container": "#d4e4ff",
    "--mb-color-tertiary": "#FFD100",
    "--mb-color-on-tertiary": "#1a1400",
    "--mb-color-tertiary-container": "#5c4a00",
    "--mb-color-on-tertiary-container": "#fff0a8",
    "--mb-color-warning": "#FFD100",
    "--mb-color-on-warning": "#1a1400",
    "--mb-color-warning-container": "#5c4a00",
    "--mb-color-on-warning-container": "#fff0a8",
    "--mb-color-success": "#3DDB7A",
    "--mb-color-on-success": "#002812",
    "--mb-color-success-container": "#0d4a28",
    "--mb-color-on-success-container": "#c8f0d4",
    "--mb-color-error": "#FF6B73",
    "--mb-color-on-error": "#3d0004",
    "--mb-color-error-container": "#7a0a12",
    "--mb-color-on-error-container": "#ffd6d8",
    "--mb-color-surface": "#0d0d0d",
    "--mb-color-surface-dim": "#0d0d0d",
    "--mb-color-surface-bright": "#2a2a2a",
    "--mb-color-surface-container-lowest": "#050505",
    "--mb-color-surface-container-low": "#141414",
    "--mb-color-surface-container": "#1a1a1a",
    "--mb-color-surface-container-high": "#242424",
    "--mb-color-surface-container-highest": "#2e2e2e",
    "--mb-color-on-surface": "#f0f0f0",
    "--mb-color-on-surface-variant": "#b0b0b0",
    "--mb-color-outline": "#f0f0f0",
    "--mb-color-outline-variant": "#4a4a4a",
    "--mb-color-shadow": "#000000",
    "--mb-color-scrim": "#000000",
    "--mb-color-inverse-surface": "#f0f0f0",
    "--mb-color-inverse-on-surface": "#111111",
    "--mb-color-inverse-primary": "#E30613",
    "--mb-color-grid": "#f0f0f0",
    "--mb-color-accent-red": "#FF3B45",
    "--mb-color-accent-blue": "#4D9FFF",
    "--mb-color-accent-yellow": "#FFD100",
    "--mb-color-scheme": "dark"
  },
  "bauhaus-weimar": {
    "--mb-color-primary": "#C41E3A",
    "--mb-color-on-primary": "#ffffff",
    "--mb-color-primary-container": "#f8c8d0",
    "--mb-color-on-primary-container": "#4a0010",
    "--mb-color-primary-fixed": "#f8c8d0",
    "--mb-color-primary-fixed-dim": "#e89aa8",
    "--mb-color-secondary": "#1A3A6B",
    "--mb-color-on-secondary": "#ffffff",
    "--mb-color-secondary-container": "#c8d6ef",
    "--mb-color-on-secondary-container": "#0a1a33",
    "--mb-color-tertiary": "#D4A017",
    "--mb-color-on-tertiary": "#1a1400",
    "--mb-color-tertiary-container": "#f5e6b8",
    "--mb-color-on-tertiary-container": "#3d2e00",
    "--mb-color-warning": "#B8860B",
    "--mb-color-on-warning": "#1a1400",
    "--mb-color-warning-container": "#f5e6b8",
    "--mb-color-on-warning-container": "#3d2e00",
    "--mb-color-success": "#2D6A3F",
    "--mb-color-on-success": "#ffffff",
    "--mb-color-success-container": "#c5e6cf",
    "--mb-color-on-success-container": "#0d2e18",
    "--mb-color-error": "#C41E3A",
    "--mb-color-on-error": "#ffffff",
    "--mb-color-error-container": "#f8c8d0",
    "--mb-color-on-error-container": "#4a0010",
    "--mb-color-surface": "#f7f0e8",
    "--mb-color-surface-dim": "#e8ddd0",
    "--mb-color-surface-bright": "#fffaf5",
    "--mb-color-surface-container-lowest": "#fffaf5",
    "--mb-color-surface-container-low": "#f3ebe1",
    "--mb-color-surface-container": "#ede3d6",
    "--mb-color-surface-container-high": "#e5d8c8",
    "--mb-color-surface-container-highest": "#dccdb8",
    "--mb-color-on-surface": "#1c1210",
    "--mb-color-on-surface-variant": "#4a3834",
    "--mb-color-outline": "#1c1210",
    "--mb-color-outline-variant": "#a89888",
    "--mb-color-shadow": "#1c1210",
    "--mb-color-scrim": "#000000",
    "--mb-color-inverse-surface": "#1c1210",
    "--mb-color-inverse-on-surface": "#f7f0e8",
    "--mb-color-inverse-primary": "#e89aa8",
    "--mb-color-grid": "#1c1210",
    "--mb-color-accent-red": "#C41E3A",
    "--mb-color-accent-blue": "#1A3A6B",
    "--mb-color-accent-yellow": "#D4A017",
    "--mb-color-scheme": "light"
  },
  "bauhaus-mono": {
    "--mb-color-primary": "#111111",
    "--mb-color-on-primary": "#ffffff",
    "--mb-color-primary-container": "#d4d4d4",
    "--mb-color-on-primary-container": "#111111",
    "--mb-color-primary-fixed": "#d4d4d4",
    "--mb-color-primary-fixed-dim": "#b0b0b0",
    "--mb-color-secondary": "#444444",
    "--mb-color-on-secondary": "#ffffff",
    "--mb-color-secondary-container": "#e0e0e0",
    "--mb-color-on-secondary-container": "#111111",
    "--mb-color-tertiary": "#FFD100",
    "--mb-color-on-tertiary": "#111111",
    "--mb-color-tertiary-container": "#fff3b0",
    "--mb-color-on-tertiary-container": "#3d3000",
    "--mb-color-warning": "#FFD100",
    "--mb-color-on-warning": "#111111",
    "--mb-color-warning-container": "#fff3b0",
    "--mb-color-on-warning-container": "#3d3000",
    "--mb-color-success": "#111111",
    "--mb-color-on-success": "#ffffff",
    "--mb-color-success-container": "#d4d4d4",
    "--mb-color-on-success-container": "#111111",
    "--mb-color-error": "#111111",
    "--mb-color-on-error": "#ffffff",
    "--mb-color-error-container": "#d4d4d4",
    "--mb-color-on-error-container": "#111111",
    "--mb-color-surface": "#f5f5f5",
    "--mb-color-surface-dim": "#e0e0e0",
    "--mb-color-surface-bright": "#ffffff",
    "--mb-color-surface-container-lowest": "#ffffff",
    "--mb-color-surface-container-low": "#f0f0f0",
    "--mb-color-surface-container": "#e8e8e8",
    "--mb-color-surface-container-high": "#dcdcdc",
    "--mb-color-surface-container-highest": "#d0d0d0",
    "--mb-color-on-surface": "#111111",
    "--mb-color-on-surface-variant": "#444444",
    "--mb-color-outline": "#111111",
    "--mb-color-outline-variant": "#888888",
    "--mb-color-shadow": "#111111",
    "--mb-color-scrim": "#000000",
    "--mb-color-inverse-surface": "#111111",
    "--mb-color-inverse-on-surface": "#f5f5f5",
    "--mb-color-inverse-primary": "#ffffff",
    "--mb-color-grid": "#111111",
    "--mb-color-accent-red": "#111111",
    "--mb-color-accent-blue": "#444444",
    "--mb-color-accent-yellow": "#FFD100",
    "--mb-color-scheme": "light"
  }
};

export function isConsoleTheme(value: string | null | undefined): value is ConsoleTheme {
  return (
    value === "bauhaus-classic" ||
    value === "bauhaus-dark" ||
    value === "bauhaus-weimar" ||
    value === "bauhaus-mono"
  );
}

/** Map legacy M3 dark/light storage values to Bauhaus packs. */
export function migrateStoredTheme(raw: string | null): ConsoleTheme {
  if (isConsoleTheme(raw)) {
    return raw;
  }
  if (raw === "light") {
    return "bauhaus-classic";
  }
  if (raw === "dark") {
    return "bauhaus-dark";
  }
  return DEFAULT_CONSOLE_THEME;
}

export function applyThemeTokens(theme: ConsoleTheme, root: HTMLElement): void {
  const tokens = { ...bauhausBaseTokens, ...themeTokens[theme] };
  Object.entries(tokens).forEach(([name, value]) => {
    root.style.setProperty(name, value);
  });

  const scheme = tokens["--mb-color-scheme"] === "dark" ? "dark" : "light";
  root.style.colorScheme = scheme;
}

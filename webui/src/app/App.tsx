import { type ReactNode } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { motion } from "motion/react";
import {
  MaterialIconButton,
  MaterialOutlinedButton
} from "../components/MaterialButton";
import { MaterialSelect } from "../components/MaterialSelect";
import { Icon } from "../components/ui/Icon";
import { type Locale, type MessageKey } from "../i18n/messages";
import { useI18n } from "../i18n/I18nProvider";
import { useConsoleTheme } from "../theme/ThemeProvider";
import type { ConsoleTheme } from "../theme/tokens";
import { pageMotion } from "../theme/motion";
import { shellStyles } from "./styles/shellStyles";
import { ConsoleAuthGate } from "./auth/ConsoleAuthGate";
import { useConsoleAuth } from "./auth/ConsoleAuthContext";

const localeOptions: Array<{ value: Locale; labelKey: MessageKey }> = [
  { value: "en-US", labelKey: "app.language.en" },
  { value: "zh-CN", labelKey: "app.language.zh" }
];

const navItems = [
  { to: "/overview", icon: "dashboard", labelKey: "nav.overview" },
  { to: "/models-providers", icon: "hub", labelKey: "nav.modelsProviders" },
  { to: "/routes", icon: "alt_route", labelKey: "nav.routes" },
  { to: "/defaults", icon: "rule_settings", labelKey: "nav.defaults" },
  { to: "/search-tools", icon: "travel_explore", labelKey: "nav.searchTools" },
  { to: "/storage", icon: "database", labelKey: "nav.storage" },
  { to: "/security", icon: "shield", labelKey: "nav.security" }
] as const;

const themeMessageKeys: Record<ConsoleTheme, MessageKey> = {
  "bauhaus-classic": "theme.bauhausClassic",
  "bauhaus-dark": "theme.bauhausDark",
  "bauhaus-weimar": "theme.bauhausWeimar",
  "bauhaus-mono": "theme.bauhausMono"
};

const themeSwatches: Record<ConsoleTheme, string> = {
  "bauhaus-classic": "#E30613",
  "bauhaus-dark": "#FF3B45",
  "bauhaus-weimar": "#C41E3A",
  "bauhaus-mono": "#111111"
};

type NavItem = (typeof navItems)[number];

export function App() {
  return (
    <>
      <style>{shellStyles}</style>
      <ConsoleAuthGate>
        <AppShell content={<Outlet />} />
      </ConsoleAuthGate>
    </>
  );
}

export function AppShell({ content }: { content?: ReactNode }) {
  return <AppShellContent content={content} />;
}

function AppShellContent({ content }: { content?: ReactNode }) {
  const { theme, themes, setTheme } = useConsoleTheme();
  const { locale, setLocale, t } = useI18n();
  const { signOut } = useConsoleAuth();

  return (
    <div className="app-shell">
      <header className="top-app-bar">
        <div className="top-app-bar__brand">
          <div className="top-app-bar__mark" aria-hidden="true">
            <span />
            <span />
            <span />
            <span />
          </div>
          <div>
            <p>Moon Bridge</p>
            <strong>{t("app.console")}</strong>
          </div>
        </div>
        <div className="top-app-bar__meta">
          <MaterialSelect
            className="locale-switch locale-switch--select"
            density="compact"
            label={t("app.language")}
            ariaLabel={t("app.language")}
            value={locale}
            options={localeOptions.map((option) => ({
              value: option.value,
              label: t(option.labelKey)
            }))}
            onChange={(value) => setLocale(value as Locale)}
          />
          <div className="theme-picker" role="group" aria-label={t("theme.picker")}>
            <span>{t("theme.picker")}</span>
            {themes.map((pack) => (
              <MaterialOutlinedButton
                key={pack}
                ariaPressed={theme === pack}
                className="theme-picker__button"
                onClick={() => setTheme(pack)}
              >
                <span
                  aria-hidden="true"
                  className="theme-picker__swatch"
                  style={{ background: themeSwatches[pack] }}
                />
                {t(themeMessageKeys[pack])}
              </MaterialOutlinedButton>
            ))}
          </div>
          <MaterialIconButton
            className="app-bar__sign-out"
            icon="lock"
            label={t("app.signOut")}
            onClick={signOut}
          />
        </div>
        <div className="top-app-bar__stripe" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      </header>

      <div className="workspace">
        <nav className="navigation-rail" aria-label={t("app.consoleSections")}>
          {navItems.map((item) => (
            <NavRailItem key={item.to} item={item} label={t(item.labelKey as MessageKey)} />
          ))}
        </nav>

        <motion.main
          aria-label={t("app.routeContent")}
          className="content-surface"
          initial={pageMotion.initial}
          animate={pageMotion.animate}
          transition={pageMotion.transition}
        >
          {content ?? <Outlet />}
        </motion.main>
      </div>
    </div>
  );
}

function NavRailItem({ item, label }: { item: NavItem; label: string }) {
  return (
    <NavLink
      to={item.to}
      className={({ isActive }) => (isActive ? "nav-item nav-item--active" : "nav-item")}
    >
      {({ isActive }) => (
        <>
          {isActive ? <span aria-hidden="true" className="nav-item__indicator" /> : null}
          <span className="nav-item__icon">
            <Icon name={item.icon} size={18} />
          </span>
          <span className="nav-item__label">{label}</span>
        </>
      )}
    </NavLink>
  );
}

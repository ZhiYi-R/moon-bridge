import type { ReactNode, SVGProps } from "react";
// ReactNode used by fallback glyph + MaterialSymbol

export type IconName =
  | "dashboard"
  | "hub"
  | "alt_route"
  | "rule_settings"
  | "travel_explore"
  | "database"
  | "shield"
  | "shield_lock"
  | "light_mode"
  | "dark_mode"
  | "lock"
  | "close"
  | "visibility"
  | "visibility_off"
  | "help"
  | "add"
  | "delete"
  | "expand_more"
  | "expand_less"
  | "refresh"
  | "search"
  | "filter_list"
  | "check"
  | "palette"
  | "arrow_drop_down"
  | "download"
  | "bolt"
  | "payments";

const paths: Record<IconName, ReactNode> = {
  dashboard: (
    <>
      <rect x="3" y="3" width="8" height="8" />
      <rect x="13" y="3" width="8" height="5" />
      <rect x="13" y="10" width="8" height="11" />
      <rect x="3" y="13" width="8" height="8" />
    </>
  ),
  hub: (
    <>
      <circle cx="12" cy="12" r="3" />
      <circle cx="5" cy="6" r="2" />
      <circle cx="19" cy="6" r="2" />
      <circle cx="5" cy="18" r="2" />
      <circle cx="19" cy="18" r="2" />
      <path d="M7 7.5 10 10M17 7.5 14 10M7 16.5 10 14M17 16.5 14 14" />
    </>
  ),
  alt_route: (
    <>
      <path d="M6 4v8a4 4 0 0 0 4 4h8" />
      <path d="M14 12l4 4-4 4" />
      <circle cx="6" cy="4" r="2" />
    </>
  ),
  rule_settings: (
    <>
      <path d="M4 6h10M4 12h16M4 18h12" />
      <circle cx="18" cy="6" r="2" />
      <circle cx="14" cy="18" r="2" />
    </>
  ),
  travel_explore: (
    <>
      <circle cx="11" cy="11" r="6" />
      <path d="m16 16 4 4" />
      <path d="M11 8v6M8 11h6" />
    </>
  ),
  database: (
    <>
      <ellipse cx="12" cy="6" rx="7" ry="3" />
      <path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6" />
      <path d="M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6" />
    </>
  ),
  shield: <path d="M12 3 5 6v5c0 4.5 2.8 7.8 7 9 4.2-1.2 7-4.5 7-9V6l-7-3z" />,
  shield_lock: (
    <>
      <path d="M12 3 5 6v5c0 4.5 2.8 7.8 7 9 4.2-1.2 7-4.5 7-9V6l-7-3z" />
      <rect x="9" y="11" width="6" height="5" />
      <path d="M10 11V9.5a2 2 0 0 1 4 0V11" />
    </>
  ),
  light_mode: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
    </>
  ),
  dark_mode: <path d="M18 13a7 7 0 1 1-7-9 5.5 5.5 0 0 0 7 9z" />,
  lock: (
    <>
      <rect x="5" y="10" width="14" height="10" />
      <path d="M8 10V7a4 4 0 0 1 8 0v3" />
    </>
  ),
  close: <path d="M6 6l12 12M18 6 6 18" />,
  visibility: (
    <>
      <path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z" />
      <circle cx="12" cy="12" r="2.5" />
    </>
  ),
  visibility_off: (
    <>
      <path d="M3 3l18 18M10.6 10.6a2.5 2.5 0 0 0 3.5 3.5" />
      <path d="M9.9 5.1A10.5 10.5 0 0 1 12 5c6.5 0 10 7 10 7a16.6 16.6 0 0 1-3.2 3.9M6.1 6.1C3.7 7.8 2 12 2 12s3.5 6 10 6c1.3 0 2.5-.2 3.6-.6" />
    </>
  ),
  help: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.5 9.5a2.5 2.5 0 1 1 3.7 2.2c-.8.5-1.2 1-1.2 2" />
      <circle cx="12" cy="17" r="0.8" fill="currentColor" stroke="none" />
    </>
  ),
  add: <path d="M12 5v14M5 12h14" />,
  delete: (
    <>
      <path d="M5 7h14M9 7V5h6v2M8 7l1 12h6l1-12" />
    </>
  ),
  expand_more: <path d="M6 9l6 6 6-6" />,
  expand_less: <path d="M6 15l6-6 6 6" />,
  refresh: <path d="M20 12a8 8 0 1 1-2.3-5.5M20 4v5h-5" />,
  search: (
    <>
      <circle cx="11" cy="11" r="6" />
      <path d="m16 16 4 4" />
    </>
  ),
  filter_list: <path d="M4 6h16M7 12h10M10 18h4" />,
  check: <path d="M5 12l5 5L19 7" />,
  palette: (
    <>
      <path d="M12 3a9 9 0 1 0 0 18h1.5a2.5 2.5 0 0 0 0-5H13a1.5 1.5 0 0 1 0-3h3A9 9 0 0 0 12 3z" />
      <circle cx="7.5" cy="10" r="1" fill="currentColor" stroke="none" />
      <circle cx="10" cy="7" r="1" fill="currentColor" stroke="none" />
      <circle cx="14" cy="7" r="1" fill="currentColor" stroke="none" />
      <circle cx="16.5" cy="10" r="1" fill="currentColor" stroke="none" />
    </>
  ),
  arrow_drop_down: <path d="M7 10l5 5 5-5z" fill="currentColor" stroke="none" />,
  download: (
    <>
      <path d="M12 4v12M7 11l5 5 5-5" />
      <path d="M5 20h14" />
    </>
  ),
  bolt: <path d="M13 2 4 14h7l-1 8 9-12h-7l1-8z" fill="currentColor" stroke="none" />,
  payments: (
    <>
      <rect x="3" y="6" width="18" height="12" />
      <path d="M3 10h18" />
      <path d="M7 15h4" />
    </>
  )
};

type IconProps = {
  name: IconName | string;
  className?: string;
  size?: number;
  title?: string;
} & Omit<SVGProps<SVGSVGElement>, "name">;

/** Extra aliases for icons still referenced as Material Symbol names in features. */
const aliases: Record<string, IconName> = {
  chevron_right: "expand_more",
  content_copy: "check",
  download: "download" as IconName,
  tag: "add",
  tune: "rule_settings",
  bolt: "bolt" as IconName,
  restart_alt: "refresh",
  list_alt: "dashboard",
  cloud_sync: "hub",
  info: "help",
  progress_activity: "refresh",
  error: "close",
  edit: "rule_settings",
  badge: "shield",
  extension: "hub",
  image: "dashboard",
  psychology: "hub",
  payments: "payments" as IconName,
  swap_horiz: "alt_route",
  south_west: "expand_more",
  north_east: "expand_less",
  sync_alt: "refresh"
};

// Geometric fallbacks for less-critical glyph names (keeps admin UI complete).
function fallbackGlyph(name: string): ReactNode {
  const n = name.length % 3;
  if (n === 0) {
    return <rect x="5" y="5" width="14" height="14" />;
  }
  if (n === 1) {
    return <circle cx="12" cy="12" r="7" />;
  }
  return <path d="M12 4 20 18H4z" />;
}

export function Icon({ name, className, size = 20, title, ...rest }: IconProps) {
  const resolved = (aliases[name] ?? name) as IconName;
  const content = paths[resolved] ?? fallbackGlyph(name);
  return (
    <svg
      aria-hidden={title ? undefined : true}
      className={className ? `bh-icon ${className}` : "bh-icon"}
      data-icon={name}
      fill="none"
      height={size}
      role={title ? "img" : undefined}
      stroke="currentColor"
      strokeLinecap="square"
      strokeLinejoin="miter"
      strokeWidth={2}
      viewBox="0 0 24 24"
      width={size}
      {...rest}
    >
      {title ? <title>{title}</title> : null}
      {content}
    </svg>
  );
}

/** Drop-in for former material-symbol spans: children is the symbol name. */
export function MaterialSymbol({
  children,
  className,
  "aria-hidden": ariaHidden = true
}: {
  children: ReactNode;
  className?: string;
  "aria-hidden"?: boolean | "true" | "false";
}) {
  const name = typeof children === "string" ? children.trim() : String(children ?? "help");
  return (
    <span className={className ? `material-symbol ${className}` : "material-symbol"} aria-hidden={ariaHidden}>
      {/* Keep the symbol name in the DOM for tests that assert pill text composition. */}
      <span className="material-symbol__name">{name}</span>
      <Icon name={name} size={18} />
    </span>
  );
}

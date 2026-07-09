import type { ReactNode } from "react";
import { ApiError } from "../rpc/http";
import { ErrorState } from "../components/ErrorState";
import { useI18n } from "../i18n/I18nProvider";
import { type ConfigPath, getConfigDescription } from "../configDocs/configDescriptions";

export const defaultPage = { limit: 20, offset: 0 };

export function PageHeader({
  eyebrow,
  title,
  children
}: {
  eyebrow: string;
  title: string;
  children?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div className="page-header__eyebrow-row">
        <span aria-hidden="true" className="page-header__mark" />
        <p className="eyebrow">{eyebrow}</p>
      </div>
      <h1>{title}</h1>
      {children ? <p>{children}</p> : null}
    </header>
  );
}

export function StoreUnavailableState() {
  const { t } = useI18n();
  return (
    <ErrorState
      title={t("error.storeTitle")}
      message={t("error.storeMessage")}
    />
  );
}

export function QueryErrorState({ error }: { error: unknown }) {
  if (error instanceof ApiError && (error.code === "store_unavailable" || error.status === 404)) {
    return <StoreUnavailableState />;
  }
  const { t } = useI18n();
  const message = error instanceof Error ? error.message : t("error.unknownRequest");
  return <ErrorState title={t("error.requestFailed")} message={message} />;
}

export function formatNumber(value: number | undefined) {
  return typeof value === "number" ? new Intl.NumberFormat().format(value) : "0";
}

export function FieldHint({ children }: { children: ReactNode }) {
  return <small className="field-hint">{children}</small>;
}

export function ConfigHint({ path, id }: { path: ConfigPath; id?: string }) {
  const { locale, t } = useI18n();
  const description = getConfigDescription(path, locale);

  return (
    <small id={id} className="field-hint">
      {description.description}
      <span> {t("configDoc.type")}: {localizedConfigMetaValue(description.type, t)}</span>
      {description.defaultValue ? (
        <span> {t("configDoc.default")}: {localizedConfigMetaValue(description.defaultValue, t)}</span>
      ) : null}
      {description.sensitive ? <span> {t("configDoc.sensitive")}</span> : null}
    </small>
  );
}

function localizedConfigMetaValue(value: string, t: ReturnType<typeof useI18n>["t"]) {
  const normalized = value.trim().toLowerCase();
  const localized: Record<string, string> = {
    boolean: t("configDoc.type.boolean"),
    empty: t("configDoc.default.empty"),
    "host:port": t("configDoc.type.hostPort"),
    number: t("configDoc.type.number"),
    object: t("configDoc.type.object"),
    string: t("configDoc.type.string"),
    url: t("configDoc.type.url")
  };
  return localized[normalized] ?? value;
}

export function FieldWithHint({
  children,
  className,
  hintPath,
  hintId
}: {
  children: ReactNode;
  className?: string;
  hintPath: ConfigPath;
  hintId: string;
}) {
  return (
    <div className={className ? `form-field ${className}` : "form-field"}>
      {children}
      <ConfigHint id={hintId} path={hintPath} />
    </div>
  );
}

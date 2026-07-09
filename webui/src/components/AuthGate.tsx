import { type FormEvent, type ReactNode, useState } from "react";
import { motion } from "motion/react";
import { MaterialFilledButton, MaterialIconButton } from "./MaterialButton";
import { MaterialCheckbox } from "./MaterialCheckbox";
import { MaterialOutlinedTextField } from "./MaterialTextField";
import { Icon } from "./ui/Icon";
import { type ApiError, isAuthError } from "../rpc/http";
import { useI18n } from "../i18n/I18nProvider";
import { springs, surfaceMotion } from "../theme/motion";

type AuthGateProps = {
  children: ReactNode;
  /** When this is a 401 error the login card is shown; otherwise children render. */
  error?: unknown;
  /** True while a submitted token is being verified — disables + relabels submit. */
  pending?: boolean;
  /** Called with the trimmed token and the remember flag on submit. */
  onSubmit?: (token: string, remember: boolean) => void | Promise<void>;
};

export function AuthGate({ children, error, pending = false, onSubmit }: AuthGateProps) {
  const { t } = useI18n();
  const [token, setToken] = useState("");
  const [remember, setRemember] = useState(false);
  const [revealed, setRevealed] = useState(false);

  if (!isAuthError(error)) {
    return <>{children}</>;
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = token.trim();
    if (!value || pending) {
      return;
    }
    await onSubmit?.(value, remember);
  }

  const apiError = error as ApiError;

  return (
    <main className="auth-gate" aria-labelledby="auth-title">
      <motion.form
        className="auth-card"
        onSubmit={submit}
        initial={surfaceMotion.initial}
        animate={surfaceMotion.animate}
        transition={surfaceMotion.transition}
      >
        <motion.span
          className="auth-card__badge"
          aria-hidden="true"
          initial={{ scale: 0.85, opacity: 0 }}
          animate={{ scale: 1, opacity: 1 }}
          transition={springs.spatial}
        >
          <Icon name="shield_lock" size={28} />
        </motion.span>
        <p className="eyebrow">{t("auth.eyebrow")}</p>
        <h1 id="auth-title">{t("auth.title")}</h1>
        <p className="auth-card__message">{apiError.message}</p>
        <MaterialOutlinedTextField
          autoFocus
          className="auth-token-field"
          label={t("auth.token")}
          spellCheck={false}
          type={revealed ? "text" : "password"}
          value={token}
          onInput={setToken}
          trailingIcon={
            <MaterialIconButton
              className="field-visibility-toggle"
              icon={revealed ? "visibility_off" : "visibility"}
              label={t(revealed ? "auth.hideToken" : "auth.revealToken")}
              onClick={() => setRevealed((current) => !current)}
              onMouseDown={(event) => event.preventDefault()}
            />
          }
        />
        <label className="auth-remember">
          <MaterialCheckbox
            checked={remember}
            label={t("auth.remember")}
            onChange={setRemember}
          />
          <span>{t("auth.remember")}</span>
        </label>
        <MaterialFilledButton className="auth-submit" type="submit" disabled={pending}>
          {pending ? t("auth.verifying") : t("action.saveToken")}
        </MaterialFilledButton>
      </motion.form>
    </main>
  );
}

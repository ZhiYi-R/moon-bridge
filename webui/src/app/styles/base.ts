export const baseStyles = `
  :root {
    color-scheme: dark;
    scrollbar-gutter: stable;
    font-family: var(--mb-font-ui, "Space Grotesk", Helvetica, Arial, sans-serif);
    font-optical-sizing: auto;
    background: var(--mb-color-surface);
    color: var(--mb-color-on-surface);
    font-synthesis: none;
    text-rendering: optimizeLegibility;
    -webkit-font-smoothing: antialiased;
  }

  :root[data-theme="bauhaus-classic"],
  :root[data-theme="bauhaus-weimar"],
  :root[data-theme="bauhaus-mono"] {
    color-scheme: light;
  }

  * {
    box-sizing: border-box;
  }

  body {
    margin: 0;
    min-width: 320px;
    min-height: 100vh;
    background: var(--mb-color-surface);
    font: var(--mb-type-body, 500 0.9375rem/1.45 sans-serif);
  }

  .bh-icon,
  .material-symbol {
    display: inline-flex;
    align-items: center;
    vertical-align: middle;
    flex-shrink: 0;
  }

  .material-symbol__name {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  ::selection {
    background: color-mix(in srgb, var(--mb-color-accent-yellow) 55%, transparent);
    color: var(--mb-color-on-surface);
  }

  :focus-visible {
    outline: 2px solid var(--mb-color-accent-yellow);
    outline-offset: 2px;
  }

  * {
    scrollbar-width: thin;
    scrollbar-color: color-mix(in srgb, var(--mb-color-outline) 55%, transparent) transparent;
  }
  *::-webkit-scrollbar {
    width: 10px;
    height: 10px;
  }
  *::-webkit-scrollbar-thumb {
    border: 2px solid transparent;
    background: color-mix(in srgb, var(--mb-color-outline) 50%, transparent);
    background-clip: padding-box;
  }
  *::-webkit-scrollbar-thumb:hover {
    background: color-mix(in srgb, var(--mb-color-outline) 80%, transparent);
    background-clip: padding-box;
  }
  *::-webkit-scrollbar-corner {
    background: transparent;
  }

  @keyframes mb-spin {
    to { transform: rotate(360deg); }
  }
  @keyframes mb-shimmer {
    0% { background-position: -480px 0; }
    100% { background-position: 480px 0; }
  }

  /* ---- Bauhaus controls ---- */
  .bh-button {
    appearance: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 40px;
    padding: 0 16px;
    border: var(--mb-border-width, 2px) solid var(--mb-color-outline);
    border-radius: 0;
    font: inherit;
    font-weight: 700;
    letter-spacing: 0.02em;
    text-transform: uppercase;
    font-size: 0.78rem;
    cursor: pointer;
    transition: background-color var(--mb-motion-standard), color var(--mb-motion-standard),
      border-color var(--mb-motion-standard), box-shadow var(--mb-motion-standard);
  }

  .bh-button:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .bh-button--filled {
    background: var(--mb-color-primary);
    color: var(--mb-color-on-primary);
    border-color: var(--mb-color-primary);
    box-shadow: var(--mb-elevation-1);
  }

  .bh-button--filled:hover:not(:disabled) {
    background: color-mix(in srgb, var(--mb-color-primary) 88%, var(--mb-color-on-surface));
  }

  .bh-button--outlined {
    background: transparent;
    color: var(--mb-color-on-surface);
    border-color: var(--mb-color-outline);
  }

  .bh-button--outlined:hover:not(:disabled) {
    background: color-mix(in srgb, var(--mb-color-primary) 10%, transparent);
    border-color: var(--mb-color-primary);
    color: var(--mb-color-primary);
  }

  .bh-button.secondary-button {
    background: var(--mb-color-surface-container);
  }

  .bh-button.fab-button {
    min-height: 40px;
    background: var(--mb-color-primary-container);
    color: var(--mb-color-on-primary-container);
    border-color: var(--mb-color-outline);
    box-shadow: var(--mb-elevation-1);
  }

  .bh-button.fab-button:hover:not(:disabled) {
    transform: translate(-1px, -1px);
    box-shadow: var(--mb-elevation-2);
  }

  .bh-button.fab-button--danger {
    background: var(--mb-color-error-container);
    color: var(--mb-color-on-error-container);
  }

  .bh-button.resource-delete-confirmation__confirm {
    background: var(--mb-color-error);
    color: var(--mb-color-on-error);
    border-color: var(--mb-color-error);
  }

  .bh-icon-button {
    appearance: none;
    display: inline-grid;
    place-items: center;
    width: 40px;
    height: 40px;
    padding: 0;
    border: var(--mb-border-width, 2px) solid transparent;
    border-radius: 0;
    background: transparent;
    color: var(--mb-color-on-surface);
    cursor: pointer;
    transition: background-color var(--mb-motion-standard), border-color var(--mb-motion-standard),
      color var(--mb-motion-standard);
  }

  .bh-icon-button:hover:not(:disabled) {
    background: color-mix(in srgb, var(--mb-color-on-surface) 8%, transparent);
    border-color: var(--mb-color-outline-variant);
    color: var(--mb-color-primary);
  }

  .bh-icon-button:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .bh-icon-button.field-visibility-toggle,
  .bh-icon-button.schema-field__help,
  .bh-icon-button.resource-field-group__toggle {
    width: 28px;
    height: 28px;
  }

  .bh-field {
    display: grid;
    gap: 6px;
    min-width: 0;
    width: 100%;
  }

  .bh-field__label {
    color: var(--mb-color-on-surface-variant);
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .bh-field__shell {
    display: flex;
    align-items: stretch;
    min-height: 44px;
    border: var(--mb-border-width, 2px) solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-lowest);
  }

  .bh-field:focus-within .bh-field__shell {
    border-color: var(--mb-color-primary);
    box-shadow: 3px 3px 0 var(--mb-color-accent-yellow);
  }

  .bh-field--error .bh-field__shell {
    border-color: var(--mb-color-error);
  }

  .bh-field--disabled {
    opacity: 0.55;
  }

  .bh-field__leading,
  .bh-field__trailing {
    display: grid;
    place-items: center;
    padding: 0 8px;
    color: var(--mb-color-on-surface-variant);
  }

  .bh-field__control {
    flex: 1;
    min-width: 0;
    width: 100%;
    border: 0;
    outline: none;
    background: transparent;
    color: var(--mb-color-on-surface);
    font: inherit;
    font-weight: 500;
    padding: 10px 12px;
    resize: vertical;
  }

  .bh-select__shell {
    position: relative;
  }

  .bh-select__native {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
    opacity: 0;
    pointer-events: none;
  }

  .bh-select__trigger {
    flex: 1;
    min-width: 0;
    min-height: 40px;
    display: inline-flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    width: 100%;
    margin: 0;
    border: 0;
    outline: none;
    background: transparent;
    color: var(--mb-color-on-surface);
    font: inherit;
    font-weight: 500;
    text-align: left;
    cursor: pointer;
    padding: 8px 10px 8px 12px;
  }

  .bh-select__trigger:focus-visible {
    outline: 2px solid var(--mb-color-primary);
    outline-offset: -2px;
  }

  .bh-select__value {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  .bh-select__value-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .bh-select__value-icon,
  .bh-select__option-icon {
    display: inline-flex;
    flex-shrink: 0;
  }

  .bh-select__chevron {
    display: inline-flex;
    flex-shrink: 0;
    color: var(--mb-color-on-surface-variant);
  }

  .bh-select__menu {
    position: absolute;
    z-index: 40;
    top: calc(100% + 4px);
    left: -2px;
    right: -2px;
    margin: 0;
    padding: 0;
    list-style: none;
    max-height: min(280px, 50vh);
    overflow: auto;
    border: 2px solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-lowest);
    box-shadow: var(--mb-elevation-3);
  }

  .bh-select__option {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 36px;
    padding: 8px 10px;
    cursor: pointer;
    color: var(--mb-color-on-surface);
    font-weight: 500;
  }

  .bh-select__option-label {
    flex: 1;
    min-width: 0;
  }

  .bh-select__option-check {
    display: inline-flex;
    color: var(--mb-color-primary);
  }

  .bh-select__option--active {
    background: color-mix(in srgb, var(--mb-color-primary) 12%, transparent);
  }

  .bh-select__option--selected {
    font-weight: 700;
  }

  .bh-select--compact .bh-select__trigger {
    min-height: 28px;
    padding: 2px 4px 2px 6px;
    font-size: 0.8rem;
    font-weight: 600;
  }

  .bh-select--compact .bh-select__menu {
    min-width: 132px;
  }

  .bh-select--compact .bh-select__option {
    min-height: 32px;
    padding: 6px 8px;
    font-size: 0.8rem;
  }

  .bh-field__support {
    margin: 0;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.75rem;
    line-height: 1.35;
  }

  .bh-field--error .bh-field__support {
    color: var(--mb-color-error);
  }

  .bh-switch {
    appearance: none;
    display: inline-flex;
    align-items: center;
    padding: 0;
    border: 0;
    background: transparent;
    cursor: pointer;
  }

  .bh-switch__track {
    position: relative;
    width: 44px;
    height: 24px;
    border: 2px solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-highest);
    transition: background-color var(--mb-motion-standard);
  }

  .bh-switch__thumb {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    background: var(--mb-color-on-surface);
    transition: transform var(--mb-motion-standard), background-color var(--mb-motion-standard);
  }

  .bh-switch--on .bh-switch__track {
    background: var(--mb-color-primary);
    border-color: var(--mb-color-primary);
  }

  .bh-switch--on .bh-switch__thumb {
    transform: translateX(20px);
    background: var(--mb-color-on-primary);
  }

  .bh-checkbox {
    appearance: none;
    width: 18px;
    height: 18px;
    margin: 0;
    border: 2px solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-lowest);
    cursor: pointer;
    vertical-align: middle;
  }

  .bh-checkbox:checked {
    background: var(--mb-color-primary);
    border-color: var(--mb-color-primary);
    background-image: linear-gradient(45deg, transparent 40%, var(--mb-color-on-primary) 40%, var(--mb-color-on-primary) 60%, transparent 60%),
      linear-gradient(-45deg, transparent 55%, var(--mb-color-on-primary) 55%, var(--mb-color-on-primary) 75%, transparent 75%);
  }

  .bh-chip-set {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }

  .bh-chip {
    appearance: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-height: 32px;
    padding: 0 12px;
    border: 2px solid var(--mb-color-outline);
    border-radius: 0;
    background: var(--mb-color-surface-container);
    color: var(--mb-color-on-surface);
    font: inherit;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    cursor: pointer;
  }

  .bh-chip--selected {
    background: var(--mb-color-primary);
    color: var(--mb-color-on-primary);
    border-color: var(--mb-color-primary);
    box-shadow: 3px 3px 0 var(--mb-color-accent-blue);
  }

  .bh-chip--assist {
    cursor: default;
    background: var(--mb-color-secondary-container);
    color: var(--mb-color-on-secondary-container);
  }

  .bh-chip--input {
    padding-right: 4px;
  }

  .bh-chip__remove {
    appearance: none;
    width: 24px;
    height: 24px;
    border: 0;
    background: transparent;
    color: inherit;
    font-size: 1rem;
    line-height: 1;
    cursor: pointer;
  }

  .bh-dialog {
    border: var(--mb-border-width-thick, 3px) solid var(--mb-color-outline);
    border-radius: 0;
    padding: 0;
    background: var(--mb-color-surface-container-high);
    color: var(--mb-color-on-surface);
    box-shadow: var(--mb-elevation-3);
    max-width: min(920px, calc(100vw - 32px));
    width: min(920px, calc(100vw - 32px));
  }

  .bh-dialog::backdrop {
    background: color-mix(in srgb, var(--mb-color-scrim) 55%, transparent);
  }

  .bh-dialog__headline,
  .material-dialog__headline {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 16px 18px;
    border-bottom: 2px solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-highest);
  }

  .bh-dialog__headline-text,
  .material-dialog__headline-text {
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    font-size: 0.95rem;
  }

  .bh-dialog__content,
  .material-dialog__content {
    padding: 18px;
    max-height: min(70vh, 720px);
    overflow: auto;
  }

  .bh-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    padding: 12px 18px 18px;
    border-top: 2px solid var(--mb-color-outline-variant);
  }

  /* ---- Shell / auth ---- */
  .app-shell {
    min-height: 100vh;
    background:
      linear-gradient(90deg, color-mix(in srgb, var(--mb-color-grid) 6%, transparent) 1px, transparent 1px),
      linear-gradient(color-mix(in srgb, var(--mb-color-grid) 6%, transparent) 1px, transparent 1px),
      var(--mb-color-surface);
    background-size: 24px 24px, 24px 24px, auto;
  }

  .auth-gate {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: 24px;
    background:
      linear-gradient(90deg, color-mix(in srgb, var(--mb-color-grid) 8%, transparent) 1px, transparent 1px),
      linear-gradient(color-mix(in srgb, var(--mb-color-grid) 8%, transparent) 1px, transparent 1px),
      var(--mb-color-surface);
    background-size: 32px 32px, 32px 32px, auto;
  }

  .auth-card {
    width: min(440px, 100%);
    display: grid;
    gap: 14px;
    border: var(--mb-border-width-thick, 3px) solid var(--mb-color-outline);
    padding: 28px;
    background: var(--mb-color-surface-container-lowest);
  }

  .auth-card__badge {
    width: 56px;
    height: 56px;
    display: grid;
    place-items: center;
    border: 2px solid var(--mb-color-outline);
    color: var(--mb-color-on-primary);
    background: var(--mb-color-primary);
  }

  .auth-card h1 {
    margin: 0;
    font: var(--mb-type-display);
    letter-spacing: var(--mb-tracking-display);
    text-transform: uppercase;
  }

  .auth-card__message {
    margin: 0;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .auth-token-field {
    width: 100%;
  }

  .auth-remember {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    width: fit-content;
    color: var(--mb-color-on-surface);
    font-size: 0.9rem;
    cursor: pointer;
    user-select: none;
  }

  .auth-submit {
    margin-top: 4px;
    width: 100%;
    min-height: 48px;
  }

  .top-app-bar {
    position: sticky;
    top: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 24px;
    min-height: var(--mb-header-height, 72px);
    padding: 0 20px;
    border-bottom: var(--mb-border-width-thick, 3px) solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-low);
    isolation: isolate;
  }

  .top-app-bar__brand {
    display: flex;
    align-items: center;
    gap: 14px;
    min-width: 0;
  }

  .top-app-bar__mark {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr 1fr;
    width: 40px;
    height: 40px;
    border: 2px solid var(--mb-color-outline);
    flex-shrink: 0;
  }

  .top-app-bar__mark span:nth-child(1) { background: var(--mb-color-accent-red); }
  .top-app-bar__mark span:nth-child(2) { background: var(--mb-color-accent-blue); }
  .top-app-bar__mark span:nth-child(3) { background: var(--mb-color-accent-yellow); }
  .top-app-bar__mark span:nth-child(4) { background: var(--mb-color-surface-container-lowest); }

  .top-app-bar p,
  .top-app-bar strong {
    margin: 0;
  }

  .top-app-bar p {
    color: var(--mb-color-on-surface-variant);
    font-size: 0.7rem;
    line-height: 1.2;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    font-weight: 700;
  }

  .top-app-bar strong {
    display: block;
    font-size: 1.2rem;
    line-height: 1.15;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .top-app-bar__stripe {
    position: absolute;
    left: 0;
    right: 0;
    bottom: -3px;
    height: 6px;
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    pointer-events: none;
  }

  .top-app-bar__stripe span:nth-child(1) { background: var(--mb-color-accent-red); }
  .top-app-bar__stripe span:nth-child(2) { background: var(--mb-color-accent-blue); }
  .top-app-bar__stripe span:nth-child(3) { background: var(--mb-color-accent-yellow); }

  .top-app-bar__meta {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 10px;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.875rem;
    white-space: nowrap;
    flex-wrap: wrap;
  }

  .locale-switch,
  .theme-picker {
    min-height: 38px;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border: 2px solid var(--mb-color-outline);
    padding: 3px;
    background: var(--mb-color-surface-container);
  }

  .locale-switch.locale-switch--select {
    min-width: 118px;
    max-width: 148px;
    min-height: 32px;
    flex-direction: row;
    align-items: center;
    gap: 6px;
    padding: 0 4px 0 8px;
  }

  .locale-switch.locale-switch--select.bh-field {
    margin: 0;
  }

  .locale-switch.locale-switch--select .bh-select__compact-label {
    flex-shrink: 0;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.65rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    line-height: 1;
  }

  .locale-switch.locale-switch--select .bh-field__shell {
    flex: 1;
    min-height: 28px;
    border: none;
    background: transparent;
    box-shadow: none;
  }

  .locale-switch.locale-switch--select .bh-select__menu {
    left: auto;
    right: -2px;
    width: max(140px, 100%);
  }

  .theme-picker > span {
    min-height: 30px;
    display: inline-flex;
    align-items: center;
    padding: 0 7px;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.7rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .theme-picker__button {
    min-width: 36px;
    min-height: 30px !important;
    padding: 0 10px !important;
    font-size: 0.7rem !important;
    box-shadow: none !important;
  }

  .theme-picker__button[aria-pressed="true"] {
    background: var(--mb-color-primary);
    color: var(--mb-color-on-primary);
    border-color: var(--mb-color-primary);
  }

  .theme-picker__swatch {
    display: inline-block;
    width: 10px;
    height: 10px;
    border: 1px solid var(--mb-color-outline);
    margin-right: 6px;
    vertical-align: middle;
  }
`;

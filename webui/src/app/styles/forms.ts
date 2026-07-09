export const formStyles = `  .form-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px 18px;
    align-items: start;
  }

  .form-grid label,
  .form-field,
  .schema-field {
    display: grid;
    gap: 6px;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.82rem;
    font-weight: 650;
    min-width: 0;
  }

  .form-field label {
    display: grid;
    gap: 6px;
    color: inherit;
    font: inherit;
  }

  .schema-field__topline {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    min-height: 26px;
  }

  .schema-field--inline {
    gap: 6px;
  }

  .schema-field__label-row {
    min-width: 0;
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .schema-field__label {
    color: inherit;
    font: inherit;
  }

  .schema-field__label {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .schema-field__required {
    margin-left: 3px;
    color: var(--mb-color-error);
  }

  .schema-field__help-wrap {
    position: relative;
    display: inline-flex;
    align-items: center;
  }

  .schema-field__help {
    min-height: 0;
    flex: 0 0 auto;
    --md-icon-button-state-layer-width: 20px;
    --md-icon-button-state-layer-height: 20px;
    --md-icon-button-state-layer-shape: 999px;
    --md-icon-button-icon-size: 16px;
    --md-icon-button-icon-color: var(--mb-color-on-surface-variant);
    --md-icon-button-hover-icon-color: var(--mb-color-primary);
    --md-icon-button-focus-icon-color: var(--mb-color-primary);
    --md-icon-button-pressed-icon-color: var(--mb-color-primary);
    --md-icon-button-hover-state-layer-color: var(--mb-color-primary);
    --md-icon-button-focus-state-layer-color: var(--mb-color-primary);
    --md-icon-button-pressed-state-layer-color: var(--mb-color-primary);
    --md-icon-button-hover-state-layer-opacity: 0.14;
    --md-icon-button-focus-state-layer-opacity: 0.14;
  }

  .rich-tooltip {
    position: fixed;
    z-index: 40;
    width: min(320px, calc(100vw - 24px));
    max-width: min(320px, 78vw);
    display: grid;
    gap: 6px;
    border-radius: 0;
    padding: 16px;
    color: var(--mb-color-on-surface-variant);
    background: var(--mb-color-surface-container-high);
    box-shadow: var(--mb-elevation-3);
    text-align: left;
    pointer-events: none;
  }

  .rich-tooltip__subhead {
    color: var(--mb-color-on-surface);
    font-size: 0.82rem;
    font-weight: 720;
    line-height: 1.3;
  }

  .rich-tooltip__body {
    font-size: 0.8rem;
    font-weight: 460;
    line-height: 1.5;
  }

  .rich-tooltip__metas {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 0;
  }

  .rich-tooltip__chip {
    display: inline-flex;
    align-items: center;
    border-radius: 0;
    padding: 2px 9px;
    background: color-mix(in srgb, var(--mb-color-primary) 14%, transparent);
    color: var(--mb-color-on-surface);
    font-size: 0.7rem;
    font-weight: 640;
    white-space: nowrap;
  }

  .schema-field__switch-line {
    width: 100%;
    min-width: 0;
    min-height: 40px;
    display: inline-flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    border-radius: 0;
    outline: 0;
    padding: 4px 6px 4px 14px;
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 60%, transparent);
  }

  .material-chip-group {
    display: flex;
    align-items: center;
    gap: 7px;
    flex-wrap: wrap;
    min-height: 38px;
  }

  .schema-structured-summary {
    width: 100%;
    display: grid;
    gap: 10px;
    border-radius: 0;
    padding: 12px;
    color: var(--mb-color-on-surface-variant);
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 58%, transparent);
  }

  .schema-structured-summary__header {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 8px;
    text-align: left;
  }

  .schema-structured-summary__header span:first-child {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--mb-color-on-surface);
  }

  .schema-structured-summary__header strong {
    min-height: 24px;
    display: inline-flex;
    align-items: center;
    border-radius: 0;
    padding: 2px 11px;
    color: var(--mb-color-on-secondary-container);
    background: var(--mb-color-secondary-container);
    font-size: 0.76rem;
    white-space: nowrap;
  }

  .schema-structured-summary__rows {
    margin: 0;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 8px;
  }

  .schema-structured-summary__row {
    min-width: 0;
    display: grid;
    gap: 3px;
    border-radius: 0;
    padding: 8px 10px;
    background: color-mix(in srgb, var(--mb-color-surface-container-highest) 54%, transparent);
  }

  .schema-structured-summary__row dt,
  .schema-structured-summary__row dd {
    margin: 0;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .schema-structured-summary__row dt {
    color: var(--mb-color-on-surface);
    font-size: 0.76rem;
    font-weight: 700;
  }

  .schema-structured-summary__row dd,
  .schema-structured-summary__empty {
    color: var(--mb-color-on-surface-variant);
    font-size: 0.78rem;
    font-weight: 520;
  }

  .schema-structured-summary__empty {
    margin: 0;
  }

  .schema-structured-object {
    width: 100%;
    display: grid;
    gap: 10px;
    border-radius: 0;
    padding: 12px;
    color: var(--mb-color-on-surface-variant);
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 58%, transparent);
  }

  .schema-structured-object__header {
    min-height: 24px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.82rem;
    font-weight: 650;
  }

  .schema-structured-object__header span:first-child {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .schema-structured-object__grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px 18px;
    align-items: start;
  }

  .schema-structured-object__boolean {
    min-height: 44px;
    align-self: center;
  }

  .schema-structured-summary--nested {
    padding: 10px;
    background: color-mix(in srgb, var(--mb-color-surface-container-highest) 42%, transparent);
  }

  .schema-field--wide {
    grid-column: 1 / -1;
  }

  .editable-list-field {
    display: grid;
    gap: 10px;
    min-width: 0;
    border-radius: 0;
    outline: 0;
    padding: 12px;
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 58%, transparent);
  }

  .editable-list-field__header {
    min-height: 24px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }

  .editable-list-field__title {
    min-width: 0;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.82rem;
    font-weight: 650;
    overflow-wrap: anywhere;
  }

  .editable-list-field__items {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    min-height: 32px;
  }

  .editable-list-field__chip {
    --md-input-chip-container-height: 32px;
    --md-input-chip-container-shape: var(--mb-shape-sm);
    --md-input-chip-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-hover-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-focus-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-pressed-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-outline-color: var(--mb-color-outline-variant);
    --md-input-chip-hover-state-layer-color: var(--mb-color-primary);
    --md-input-chip-focus-state-layer-color: var(--mb-color-primary);
    --md-input-chip-pressed-state-layer-color: var(--mb-color-primary);
    --md-input-chip-hover-state-layer-opacity: 0.08;
    --md-input-chip-focus-state-layer-opacity: 0.08;
    --md-input-chip-icon-size: 18px;
    --md-input-chip-trailing-icon-color: var(--mb-color-on-surface-variant);
    --md-input-chip-hover-trailing-icon-color: var(--mb-color-primary);
    --md-input-chip-focus-trailing-icon-color: var(--mb-color-primary);
    --md-input-chip-pressed-trailing-icon-color: var(--mb-color-primary);
  }

  .editable-list-field__composer {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: stretch;
    gap: 10px;
  }

  .structured-feature-field {
    display: grid;
    gap: 12px;
    min-width: 0;
    border-radius: 0;
    outline: 0;
    padding: 12px;
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 58%, transparent);
  }

  .structured-feature-field__grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px 18px;
    align-items: start;
  }

  .structured-feature-field__grid--wide {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .structured-feature-field__number {
    width: 100%;
  }

  .structured-feature-field__secret {
    width: 100%;
  }

  .extension-feature-list {
    display: grid;
    grid-template-columns: 1fr;
    gap: 8px;
    min-height: 32px;
  }

  .extension-feature-row {
    min-width: 0;
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: 8px;
    border-radius: 0;
    padding: 8px;
    background: color-mix(in srgb, var(--mb-color-surface-container-highest) 48%, transparent);
  }

  .extension-config-grid {
    grid-column: 1 / -1;
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    min-width: 0;
  }

  .extension-feature-row__chip {
    min-width: 0;
    --md-input-chip-container-height: 32px;
    --md-input-chip-container-shape: var(--mb-shape-sm);
    --md-input-chip-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-hover-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-focus-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-pressed-label-text-color: var(--mb-color-on-surface);
    --md-input-chip-outline-color: var(--mb-color-outline-variant);
    --md-input-chip-hover-state-layer-color: var(--mb-color-primary);
    --md-input-chip-focus-state-layer-color: var(--mb-color-primary);
    --md-input-chip-pressed-state-layer-color: var(--mb-color-primary);
    --md-input-chip-hover-state-layer-opacity: 0.08;
    --md-input-chip-focus-state-layer-opacity: 0.08;
    --md-input-chip-icon-size: 18px;
    --md-input-chip-trailing-icon-color: var(--mb-color-on-surface-variant);
    --md-input-chip-hover-trailing-icon-color: var(--mb-color-primary);
    --md-input-chip-focus-trailing-icon-color: var(--mb-color-primary);
    --md-input-chip-pressed-trailing-icon-color: var(--mb-color-primary);
  }

  .extension-feature-row__switch {
    display: inline-flex;
    align-items: center;
    justify-content: flex-end;
  }

  .extension-feature-row__switch md-switch {
    --md-switch-selected-track-color: var(--mb-color-primary);
    --md-switch-selected-handle-color: var(--mb-color-on-primary);
    --md-switch-selected-hover-track-color: var(--mb-color-primary);
    --md-switch-selected-focus-track-color: var(--mb-color-primary);
    --md-switch-selected-pressed-track-color: var(--mb-color-primary);
  }

  .editable-list-field__input {
    width: 100%;
  }

  .editable-list-field__add {
    align-self: stretch;
    --md-filled-button-container-height: 44px;
    --md-filled-button-container-shape: var(--mb-button-shape);
    --md-filled-button-leading-space: 12px;
    --md-filled-button-trailing-space: 14px;
  }

  .field-status {
    display: inline-flex;
    align-items: center;
    justify-self: end;
    gap: 6px;
    min-height: 24px;
    border-radius: 0;
    padding: 2px 11px;
    color: var(--mb-color-on-surface-variant);
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 78%, transparent);
    font-size: 0.72rem;
    font-weight: 700;
    white-space: nowrap;
  }

  @keyframes mb-blink {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.35; }
  }

  .field-status--saving .field-status__dot {
    animation: mb-blink 1s var(--mb-ease-standard) infinite;
  }

  .field-status__dot {
    width: 7px;
    height: 7px;
    border-radius: 999px;
    background: currentColor;
  }

  .field-status--dirty {
    color: var(--mb-color-on-tertiary-container);
    background: var(--mb-color-tertiary-container);
  }

  .field-status--saving {
    color: var(--mb-color-on-primary-container);
    background: var(--mb-color-primary-container);
  }

  .field-status--saved,
  .field-status--idle {
    color: var(--mb-color-on-surface-variant);
    background: transparent;
    font-weight: 600;
  }

  .field-status--saved .field-status__dot,
  .field-status--idle .field-status__dot {
    color: var(--mb-color-success);
  }

  .field-status--error {
    color: var(--mb-color-on-error-container);
    background: var(--mb-color-error-container);
  }

  .mb-field {
    position: relative;
    display: grid;
    gap: 4px;
    min-width: 0;
  }

  .mb-field__control {
    position: relative;
    display: flex;
    align-items: stretch;
    min-width: 0;
  }

  .mb-field md-outlined-text-field {
    width: 100%;
    min-width: 0;
    --md-outlined-text-field-input-text-color: var(--mb-color-on-surface);
    --md-outlined-text-field-input-text-size: 0.88rem;
    --md-outlined-text-field-input-text-weight: 640;
    --md-outlined-text-field-label-text-color: var(--mb-color-on-surface-variant);
    --md-outlined-text-field-focus-label-text-color: var(--mb-color-primary);
    --md-outlined-text-field-outline-color: var(--mb-color-outline);
    --md-outlined-text-field-hover-outline-color: var(--mb-color-on-surface);
    --md-outlined-text-field-focus-outline-color: var(--mb-color-primary);
    --md-outlined-text-field-error-outline-color: var(--mb-color-error);
    --md-outlined-text-field-error-focus-outline-color: var(--mb-color-error);
    --md-outlined-text-field-error-label-text-color: var(--mb-color-error);
    --md-outlined-text-field-error-focus-label-text-color: var(--mb-color-error);
    --md-outlined-text-field-leading-icon-color: var(--mb-color-on-surface-variant);
    --md-outlined-text-field-focus-leading-icon-color: var(--mb-color-on-surface-variant);
    --md-outlined-text-field-trailing-icon-color: var(--mb-color-on-surface-variant);
    --md-outlined-text-field-focus-trailing-icon-color: var(--mb-color-primary);
    --md-outlined-text-field-supporting-text-color: var(--mb-color-on-surface-variant);
    --md-outlined-text-field-error-supporting-text-color: var(--mb-color-error);
  }

  .material-field-leading-node,
  .material-select-leading-node,
  .material-select-option-icon {
    display: inline-grid;
    place-items: center;
    width: 20px;
    height: 20px;
    color: currentColor;
    line-height: 1;
  }

  .material-field-leading-node svg,
  .material-select-leading-node svg,
  .material-select-option-icon svg {
    display: block;
    width: 18px;
    height: 18px;
  }

  md-outlined-text-field.material-text-field--single-line {
    --md-outlined-text-field-top-space: 12px;
    --md-outlined-text-field-bottom-space: 12px;
    --md-outlined-text-field-input-text-line-height: 1.25rem;
  }

  md-filled-text-field.material-text-field--single-line {
    --md-filled-text-field-top-space: 12px;
    --md-filled-text-field-bottom-space: 12px;
    --md-filled-text-field-with-label-top-space: 4px;
    --md-filled-text-field-with-label-bottom-space: 4px;
    --md-filled-text-field-input-text-line-height: 1.25rem;
  }

  .mb-field[data-variant="textarea"] md-outlined-text-field {
    --md-outlined-text-field-input-text-font: var(--mb-font-mono);
    --md-outlined-text-field-input-text-line-height: 1.45;
  }

  .mb-field[data-variant="textarea"] md-outlined-text-field {
    min-height: 132px;
  }

  .mb-field__required {
    margin-left: 2px;
    color: var(--mb-color-error);
  }

  md-outlined-select.material-select--single-line {
    --md-outlined-select-text-field-input-text-line-height: 1.25rem;
    --md-outlined-field-top-space: 12px;
    --md-outlined-field-bottom-space: 12px;
  }

  .mb-field md-outlined-select {
    width: 100%;
    min-width: 0;
    --md-outlined-select-text-field-input-text-color: var(--mb-color-on-surface);
    --md-outlined-select-text-field-input-text-size: 0.88rem;
    --md-outlined-select-text-field-input-text-weight: 640;
    --md-outlined-select-text-field-label-text-color: var(--mb-color-on-surface-variant);
    --md-outlined-select-text-field-focus-label-text-color: var(--mb-color-primary);
    --md-outlined-select-text-field-outline-color: var(--mb-color-outline);
    --md-outlined-select-text-field-hover-outline-color: var(--mb-color-on-surface);
    --md-outlined-select-text-field-focus-outline-color: var(--mb-color-primary);
    --md-outlined-select-text-field-leading-icon-color: var(--mb-color-on-surface-variant);
    --md-outlined-select-text-field-focus-leading-icon-color: var(--mb-color-on-surface-variant);
    --md-outlined-select-text-field-trailing-icon-color: var(--mb-color-on-surface-variant);
    --md-outlined-select-text-field-focus-trailing-icon-color: var(--mb-color-primary);
    --md-menu-container-color: var(--mb-color-surface-container-high);
    --md-menu-container-shape: var(--mb-shape-md);
    --md-menu-container-elevation: 2;
  }

  .mb-field__select-actions {
    min-height: 20px;
    display: flex;
    justify-content: flex-end;
    align-items: center;
    margin-bottom: -2px;
    pointer-events: none;
  }

  .mb-field__select-help {
    min-height: 0;
    flex: 0 0 auto;
    pointer-events: auto;
    --md-icon-button-state-layer-width: 20px;
    --md-icon-button-state-layer-height: 20px;
    --md-icon-button-state-layer-shape: 999px;
    --md-icon-button-icon-size: 16px;
    --md-icon-button-icon-color: var(--mb-color-on-surface-variant);
    --md-icon-button-hover-icon-color: var(--mb-color-primary);
    --md-icon-button-focus-icon-color: var(--mb-color-primary);
    --md-icon-button-pressed-icon-color: var(--mb-color-primary);
    --md-icon-button-hover-state-layer-color: var(--mb-color-primary);
    --md-icon-button-focus-state-layer-color: var(--mb-color-primary);
    --md-icon-button-pressed-state-layer-color: var(--mb-color-primary);
    --md-icon-button-hover-state-layer-opacity: 0.14;
    --md-icon-button-focus-state-layer-opacity: 0.14;
  }

  .form-field--create-track.mb-field {
    display: grid;
  }

  .form-grid .mb-field--wide,
  .mb-field--wide {
    min-width: 0;
  }

  .form-grid__wide,
  .form-actions {
    grid-column: 1 / -1;
  }

  .form-grid__compact {
    grid-column: span 1;
  }

  .form-grid__medium {
    grid-column: span 1;
  }

  .form-grid--route-identity {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .form-grid__reasoning-defaults {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 18px;
  }

  @media (max-width: 1080px) {
    .form-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 680px) {
    .form-grid {
      grid-template-columns: 1fr;
    }

    .structured-feature-field__grid {
      grid-template-columns: 1fr;
    }

    .schema-structured-object__grid {
      grid-template-columns: 1fr;
    }

    .editable-list-field__composer {
      grid-template-columns: 1fr;
    }
  }

  .form-actions {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
  }

  .feedback-inline,
  .feedback-banner {
    color: var(--mb-color-primary);
    font-weight: 650;
  }

  .edit-state-banner {
    margin: 0;
    border: 1px solid color-mix(in srgb, var(--mb-color-primary) 40%, transparent);
    border-radius: 0;
    padding: 14px 16px;
    background: color-mix(in srgb, var(--mb-color-primary-container) 42%, var(--mb-color-surface));
  }

  .field-hint {
    display: block;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.76rem;
    line-height: 1.45;
    font-weight: 500;
    overflow-wrap: anywhere;
  }

  .field-hint span {
    display: inline-block;
  }

  .field-error {
    margin: 0;
    border-radius: 0;
    padding: 8px 12px;
    color: var(--mb-color-on-error-container);
    background: var(--mb-color-error-container);
    font-size: 0.76rem;
    line-height: 1.4;
    font-weight: 650;
    overflow-wrap: anywhere;
  }

  .field-warning {
    margin: 0;
    border-radius: 0;
    padding: 8px 12px;
    color: var(--mb-color-on-warning-container);
    background: var(--mb-color-warning-container);
    font-size: 0.76rem;
    line-height: 1.4;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .field-error--sr {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: -1px;
    border: 0;
    padding: 0;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }

  .json-block {
    max-height: 420px;
    overflow: auto;
    margin: 0;
    border-radius: 0;
    padding: 16px;
    color: var(--mb-color-on-surface);
    background: var(--mb-color-surface-container-lowest);
    font-family: var(--mb-font-mono);
    font-size: 0.82rem;
    line-height: 1.45;
  }

`;

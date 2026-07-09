export const logStyles = `  .logs-panel__header {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: 14px;
    margin-bottom: 14px;
  }

  .logs-panel__header h2 {
    margin: 0;
  }

  .logs-panel__actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    flex-wrap: wrap;
  }

  .logs-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    flex-wrap: wrap;
    margin-bottom: 14px;
  }

  .logs-toolbar__actions {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .logs-count {
    min-height: 32px;
    display: inline-flex;
    align-items: center;
    margin: 0;
    border-radius: 0;
    padding: 0 14px;
    color: var(--mb-color-on-surface-variant);
    background: var(--mb-color-surface-container-high);
    font-size: 0.82rem;
    font-weight: 650;
  }

  .logs-chip-set {
    display: flex;
    align-items: center;
    gap: 7px;
    flex-wrap: wrap;
  }

  .log-level-filter {
    margin-bottom: 14px;
  }

  .logs-stream-status {
    margin: 0 0 14px;
    border: 1px solid color-mix(in srgb, var(--mb-color-error) 45%, transparent);
    border-radius: 0;
    padding: 12px 14px;
    color: var(--mb-color-on-error-container);
    background: var(--mb-color-error-container);
    font-size: 0.85rem;
    font-weight: 650;
  }

  .logs-search {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: 8px;
    margin-bottom: 14px;
  }

  .logs-search__field {
    width: 100%;
  }

  .material-symbol {
    font-family: "Material Symbols Rounded", "Material Symbols Outlined", sans-serif;
    font-size: 1.15rem;
    line-height: 1;
  }

  .log-output {
    max-height: min(46vh, 600px);
    overflow: auto;
    display: grid;
    gap: 4px;
    border-radius: 0;
    outline: 0;
    padding: 10px;
    background: var(--mb-color-surface-container-lowest);
  }

  .log-empty-state {
    min-height: 180px;
    display: grid;
    place-items: center;
    margin: 0;
    border-radius: 0;
    outline: 0;
    padding: 18px;
    color: var(--mb-color-on-surface-variant);
    background: color-mix(in srgb, var(--mb-color-surface-container) 48%, transparent);
    text-align: center;
    font-size: 0.9rem;
    font-weight: 650;
  }

  .log-row {
    display: grid;
    grid-template-columns: auto auto minmax(0, 1fr);
    align-items: baseline;
    gap: 10px;
    border-radius: 0;
    padding: 7px 12px;
    background: color-mix(in srgb, var(--mb-color-surface-container) 58%, transparent);
    transition: background-color var(--mb-duration-short) var(--mb-ease-standard);
  }

  .log-row:hover {
    background: color-mix(in srgb, var(--mb-color-surface-container) 84%, transparent);
  }

  .log-row__level {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    justify-self: start;
    min-width: 56px;
    border-radius: 0;
    padding: 2px 8px;
    color: var(--mb-color-on-surface-variant);
    background: var(--mb-color-surface-container-high);
    font-size: 0.66rem;
    font-weight: 760;
    letter-spacing: 0.05em;
    text-align: center;
    text-transform: uppercase;
  }

  .log-row__level--error {
    color: var(--mb-color-on-error-container);
    background: var(--mb-color-error-container);
  }

  .log-row__level--warn {
    color: var(--mb-color-on-warning-container);
    background: var(--mb-color-warning-container);
  }

  .log-row__level--info {
    color: var(--mb-color-on-primary-container);
    background: var(--mb-color-primary-container);
  }

  .log-row__level--debug {
    color: var(--mb-color-on-secondary-container);
    background: var(--mb-color-secondary-container);
  }

  .log-row__time {
    font-family: var(--mb-font-mono);
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--mb-color-on-surface-variant);
    white-space: nowrap;
  }

  .log-row__message {
    margin: 0;
    min-width: 0;
    color: var(--mb-color-on-surface);
    font-family: var(--mb-font-mono);
    font-size: 0.8rem;
    line-height: 1.4;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  @media (max-width: 600px) {
    .log-row {
      grid-template-columns: auto minmax(0, 1fr);
    }
    .log-row__time {
      display: none;
    }
  }

`;


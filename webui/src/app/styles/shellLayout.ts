export const shellLayoutStyles = `
  .workspace {
    display: grid;
    grid-template-columns: var(--mb-nav-width, 200px) minmax(0, 1fr);
    min-height: calc(100vh - var(--mb-header-height, 72px));
  }

  .navigation-rail {
    position: sticky;
    top: var(--mb-header-height, 72px);
    align-self: start;
    height: calc(100vh - var(--mb-header-height, 72px));
    display: flex;
    flex-direction: column;
    gap: 0;
    padding: 0;
    outline: 0;
    border-right: var(--mb-border-width-thick, 3px) solid var(--mb-color-outline);
    background: var(--mb-color-surface-container-low);
    overflow-y: auto;
  }

  .nav-item {
    position: relative;
    width: 100%;
    min-height: 56px;
    display: grid;
    grid-template-columns: 40px minmax(0, 1fr);
    align-items: center;
    gap: 10px;
    padding: 10px 14px 10px 12px;
    border-bottom: 1px solid color-mix(in srgb, var(--mb-color-outline) 35%, transparent);
    color: var(--mb-color-on-surface-variant);
    text-decoration: none;
    -webkit-tap-highlight-color: transparent;
    transition: color var(--mb-motion-standard), background-color var(--mb-motion-standard);
  }

  .nav-item__icon {
    position: relative;
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border: 2px solid var(--mb-color-outline-variant);
    background: var(--mb-color-surface-container);
    overflow: hidden;
  }

  .nav-item__indicator {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 5px;
    background: var(--mb-color-primary);
  }

  .nav-item__label {
    max-width: none;
    font-size: 0.78rem;
    line-height: 1.2;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    text-align: left;
    white-space: normal;
  }

  .nav-item:hover {
    color: var(--mb-color-on-surface);
    background: color-mix(in srgb, var(--mb-color-primary) 8%, transparent);
  }

  .nav-item:hover .nav-item__icon {
    border-color: var(--mb-color-primary);
  }

  .nav-item--active {
    color: var(--mb-color-on-surface);
    background: color-mix(in srgb, var(--mb-color-primary) 12%, transparent);
  }

  .nav-item--active .nav-item__icon {
    color: var(--mb-color-on-primary);
    background: var(--mb-color-primary);
    border-color: var(--mb-color-primary);
  }

  .nav-item--active .nav-item__label {
    color: var(--mb-color-on-surface);
    font-weight: 700;
  }

  .nav-item:focus-visible {
    outline: 2px solid var(--mb-color-accent-yellow);
    outline-offset: -2px;
  }

  .content-surface {
    min-width: 0;
    padding: 28px var(--mb-content-gutter);
  }

  .placeholder-panel {
    min-height: calc(100vh - 120px);
    display: flex;
    align-items: center;
    border: var(--mb-border-width, 2px) solid var(--mb-color-outline);
    padding: 32px;
    background: var(--mb-color-surface-container);
    box-shadow: var(--mb-elevation-1);
  }

  .placeholder-panel > div {
    max-width: 760px;
  }

  .eyebrow {
    margin: 0 0 10px;
    color: var(--mb-color-primary);
    font-size: 0.72rem;
    font-weight: 700;
    letter-spacing: 0.14em;
    text-transform: uppercase;
  }

  .page-header__eyebrow-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 8px;
  }

  .page-header__mark {
    width: 14px;
    height: 14px;
    background: var(--mb-color-primary);
    border: 2px solid var(--mb-color-outline);
    flex-shrink: 0;
  }

  h1 {
    margin: 0;
    font-size: clamp(1.8rem, 3.2vw, 2.6rem);
    line-height: 1.05;
    font-weight: 700;
    letter-spacing: 0.02em;
    text-transform: uppercase;
  }

  .placeholder-panel p:last-child {
    margin: 18px 0 0;
    max-width: 620px;
    color: var(--mb-color-on-surface-variant);
    font-size: 1rem;
    line-height: 1.6;
  }

  .page-stack {
    display: grid;
    gap: 20px;
    width: min(100%, var(--mb-content-max));
    margin-inline: auto;
  }

  .page-header {
    max-width: none;
    padding-bottom: 8px;
    border-bottom: 2px solid var(--mb-color-outline);
  }

  .page-header h1 {
    font: var(--mb-type-display);
    letter-spacing: var(--mb-tracking-display);
  }

  .page-header p:last-child {
    margin: 12px 0 0;
    max-width: 68ch;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.95rem;
    line-height: 1.55;
  }
`;

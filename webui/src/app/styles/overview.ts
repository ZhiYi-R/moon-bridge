export const overviewStyles = `  .metric-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 12px;
  }

  .usage-dashboard {
    display: grid;
    gap: 16px;
  }

  .panel-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
  }

  .panel-heading h2,
  .panel-heading p {
    margin: 0;
  }

  .panel-heading h2 {
    font-size: 1rem;
    line-height: 1.25;
  }

  .panel-heading p {
    margin-top: 5px;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.86rem;
    line-height: 1.45;
  }

  .usage-heading-controls {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 10px;
    flex-wrap: wrap;
  }

  .usage-range {
    display: flex;
    align-items: center;
    gap: 7px;
    flex-wrap: wrap;
  }

  .usage-demo-toggle {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-height: 32px;
    padding: 0 12px;
    border-radius: 0;
    color: var(--mb-color-on-surface-variant);
    background: var(--mb-color-surface-container);
    font-size: 0.78rem;
    font-weight: 650;
    cursor: pointer;
    user-select: none;
  }

  .usage-summary-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(168px, 1fr));
    gap: 12px;
  }

  .usage-metric {
    position: relative;
    min-width: 0;
    display: grid;
    gap: 8px;
    border-radius: 0;
    outline: 0;
    padding: 14px 16px;
    background: var(--mb-color-surface-container-high);
    transition:
      background-color var(--mb-duration-medium) var(--mb-ease-standard);
  }

  .usage-metric:hover {
    background: var(--mb-color-surface-container-highest);
  }

  .usage-metric__icon {
    position: absolute;
    top: 14px;
    right: 14px;
    width: 36px;
    height: 36px;
    display: grid;
    place-items: center;
    border-radius: 0;
    color: var(--mb-color-on-primary-container);
    background: var(--mb-color-primary-container);
  }

  .usage-metric__icon.material-symbol {
    font-size: 20px;
  }

  .usage-metric--tertiary .usage-metric__icon {
    color: var(--mb-color-on-tertiary-container);
    background: var(--mb-color-tertiary-container);
  }

  .usage-metric--secondary .usage-metric__icon {
    color: var(--mb-color-on-secondary-container);
    background: var(--mb-color-secondary-container);
  }

  .usage-metric__label {
    padding-right: 44px;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.72rem;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .usage-metric strong {
    min-width: 0;
    padding-right: 44px;
    overflow-wrap: anywhere;
    font-size: 1.32rem;
    line-height: 1.12;
    font-weight: 650;
  }

  .usage-chart-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(248px, 1fr));
    gap: 12px;
  }

  .usage-chart {
    min-width: 0;
    display: grid;
    gap: 12px;
    border-radius: 0;
    outline: 0;
    padding: 16px;
    background: color-mix(in srgb, var(--mb-color-surface-container-high) 76%, var(--mb-color-surface));
  }

  .usage-chart:focus-visible {
    background: var(--mb-color-surface-container-high);
  }

  .usage-chart__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }

  .usage-chart__header h3 {
    margin: 0;
    font-size: 0.88rem;
    line-height: 1.25;
  }

  .usage-chart__header span {
    color: var(--mb-color-on-surface-variant);
    font-size: 0.78rem;
    font-weight: 700;
  }

  .usage-chart__bar {
    overflow: hidden;
    display: flex;
    gap: 0;
    /* Fixed height (not min-height) so the bar never stretches when the panel
       around it grows; only the outer ends are rounded via the container. */
    block-size: 10px;
    border-radius: 0;
    background: var(--mb-color-surface-container);
  }

  .usage-chart__segment {
    min-inline-size: 4px;
    border-radius: 0;
    transition: inline-size var(--mb-duration-long) var(--mb-ease-decelerate);
  }

  .usage-segment--input {
    background: var(--mb-color-primary);
  }

  .usage-segment--output {
    background: var(--mb-color-tertiary);
  }

  .usage-segment--cache-write {
    background: var(--mb-color-secondary);
  }

  .usage-segment--cache-read {
    background: var(--mb-color-primary-container);
  }

  .usage-segment--cost-1 {
    background: #7c6fdd;
  }

  .usage-segment--cost-2 {
    background: #2f8f68;
  }

  .usage-segment--cost-3 {
    background: #c26a30;
  }

  .usage-segment--cost-4 {
    background: #b84f76;
  }

  .usage-segment--cost-5 {
    background: #4f82c8;
  }

  .usage-segment--cost-6 {
    background: #8a7a24;
  }

  .usage-chart__legend {
    display: grid;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .usage-chart__legend li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 8px;
    align-items: center;
    color: var(--mb-color-on-surface-variant);
    font-size: 0.78rem;
  }

  .usage-chart__legend li > span:not(.usage-chart__dot) {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .usage-chart__legend strong {
    color: var(--mb-color-on-surface);
    font-weight: 700;
  }

  .usage-chart__dot {
    width: 10px;
    height: 10px;
    border-radius: 0;
  }

  .usage-table td {
    white-space: nowrap;
  }

  .usage-table td:first-child,
  .usage-table td:nth-child(2) {
    max-width: 260px;
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .usage-empty-state {
    min-height: 180px;
    display: grid;
    place-items: center;
    border-radius: 0;
    outline: 0;
    color: var(--mb-color-on-surface-variant);
    background: color-mix(in srgb, var(--mb-color-surface-container) 48%, transparent);
    font-weight: 650;
  }

  .usage-empty-state p {
    margin: 0;
  }

  .overview-logs {
    display: grid;
    gap: 16px;
  }

`;

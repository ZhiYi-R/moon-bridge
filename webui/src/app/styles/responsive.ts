export const responsiveStyles = `  @media (max-width: 760px) {
    .top-app-bar {
      align-items: flex-start;
      flex-direction: column;
      padding: 14px 16px;
    }

    .top-app-bar__meta {
      width: 100%;
      flex-wrap: wrap;
      justify-content: flex-start;
      white-space: normal;
    }

    .workspace {
      grid-template-columns: 1fr;
      align-content: start;
      min-height: 0;
    }

    .navigation-rail {
      position: static;
      top: auto;
      z-index: 1;
      height: auto;
      flex-direction: row;
      align-items: stretch;
      justify-content: flex-start;
      overflow-x: auto;
      padding: 10px 12px;
      scroll-snap-type: x proximity;
    }

    .nav-item {
      flex: 0 0 108px;
      scroll-snap-align: start;
    }

    .content-surface {
      padding: 16px;
    }

    .log-output {
      max-height: 360px;
    }

    .placeholder-panel {
      min-height: 440px;
      padding: 24px;
    }

    .metric-grid,
    .section-grid,
    .usage-summary-grid,
    .usage-chart-grid {
      grid-template-columns: 1fr;
    }

    .compact-list li {
      grid-template-columns: 1fr;
      gap: 4px;
    }

    .resource-table {
      min-width: 640px;
    }

    .resource-editor-card__header {
      display: grid;
    }

    .resource-editor-card__meta {
      min-width: 0;
      justify-content: flex-start;
    }

    .resource-editor-card__status {
      justify-content: flex-start;
    }

    .section-heading {
      display: grid;
    }

    .create-resource {
      justify-items: stretch;
    }

    .create-resource__add {
      justify-content: center;
      width: 100%;
    }

    .form-grid,
    .section-grid {
      grid-template-columns: 1fr;
    }

    .form-grid__compact,
    .form-grid__medium,
    .form-grid__wide,
    .form-grid.create-resource__fields .form-field--create-track {
      grid-column: 1 / -1;
      max-width: none;
    }

    .form-grid__reasoning-defaults {
      grid-template-columns: 1fr;
    }

    .form-grid.create-resource__fields .create-resource__context-window-row {
      grid-template-columns: 1fr;
    }

  }

  @media (prefers-reduced-motion: reduce) {
    *,
    *::before,
    *::after {
      scroll-behavior: auto !important;
      transition-duration: 0.01ms !important;
      animation-duration: 0.01ms !important;
      animation-iteration-count: 1 !important;
    }

    .resource-editor-card:hover,
    .usage-metric:hover,
    .bh-button:active,
    .nav-item:hover .nav-item__icon .bh-icon {
      transform: none !important;
    }
  }
`;

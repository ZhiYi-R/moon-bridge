# Webui Component Policy (Bauhaus)

As of the Bauhaus console redesign (`design/bauhaus-console`), common controls are **custom Bauhaus components** under `webui/src/components/ui/`.

The previous rule that required official Material Web (`@material/web`) hosts for buttons, fields, selects, switches, chips, and dialogs **no longer applies**.

## Replacement map

| Former Material wrapper | Bauhaus control |
| --- | --- |
| `MaterialButton` | `components/ui/Button` (re-exported under Material* names) |
| `MaterialTextField` | `components/ui/TextField` |
| `MaterialSelect` | `components/ui/Select` |
| `MaterialSwitch` / `Checkbox` / chips / dialog | `components/ui/*` |

## Review requirements

- Prefer accessible native controls (`button`, `input`, `select`, `dialog`) styled via CSS variables.
- Theme packs live in `webui/src/theme/tokens.ts` (`bauhaus-classic|dark|weimar|mono`).
- Tests should query roles, labels, and `.bh-*` classes rather than `md-*` custom elements.

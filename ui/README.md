# UI Packages

`ui/` contains user interfaces for GoClaw.

## Layout

- `web/`: React web management UI.
- `desktop/`: Desktop shell and desktop-specific frontend integration.

## Guidance

The existing `ui/web` app is primarily an operator/admin console. Consumer-facing product surfaces, such as Closy, should use a separate route group and product shell rather than adding more admin navigation.

When sharing components, keep low-level reusable UI in `ui/web/src/components/` and product-specific flows under `ui/web/src/pages/<product>/`.

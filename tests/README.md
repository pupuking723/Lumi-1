# Tests

`tests/` contains cross-package and scenario-level tests. Package-local unit tests should stay next to the code they cover.

## Layout

- `contracts/`: API, schema, and WebSocket contract tests.
- `integration/`: Integration tests that exercise multiple subsystems.
- `invariants/`: Repo-wide invariants and safety checks.
- `scenarios/`: Scenario-oriented end-to-end test material.
- `zalo_e2e/`: Zalo-specific end-to-end tests.

## Guidance

For cleanup or directory moves, add the smallest targeted regression test that protects the behavior being moved. Avoid broad unrelated rewrites in the same change.

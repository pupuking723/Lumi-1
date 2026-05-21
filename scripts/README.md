# Scripts

`scripts/` contains install, setup, release, and maintenance scripts.

Scripts are part of the public operator surface, so paths referenced in docs, Docker images, install snippets, and release workflows should be treated as compatibility-sensitive.

## Guidance

- Prefer adding new scripts here instead of scattering shell helpers in the repo root.
- Keep root-level scripts only when they are primary entrypoints documented for users.
- If a script is moved, update docs, CI, Dockerfiles, and release instructions in the same change.

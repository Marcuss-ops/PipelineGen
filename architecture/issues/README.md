# Generated issues registry

`architecture/issues.yaml` is generated from `architecture/catalog.yaml` by the
canonical admin regeneration command. It currently contains only the active
issue projection and is already substantially smaller than the historical
826-line snapshot.

Do not split or edit `architecture/issues.yaml` directly. Split large ownership
or planning sections in `architecture/catalog.yaml`, then regenerate the derived
views through `cmd/admin/regen-current-yaml` so issue status remains single-source.

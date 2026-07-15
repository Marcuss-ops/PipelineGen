# Ownership inventory shards

`architecture/ownership.generated.yaml` is a compatibility artifact and must not
be edited or split manually. Its canonical sources are:

1. `modules.yaml`
2. `jobs.yaml`
3. `services.yaml`
4. `application.yaml`
5. `infrastructure.yaml`
6. `packages.yaml`

Regenerate the aggregate with:

```sh
go run ./cmd/architecture-aggregate
```

Validate that the committed aggregate matches the shards with:

```sh
go run ./cmd/architecture-aggregate --dry-run
```

# Codegen

Wraps [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) to produce
one Go package per spec under `internal/<service>/`.

```bash
bash regen.sh
```

The script iterates `../spec/services/*.yaml` and emits to `../internal/<slug>/`.
Place per-service config tweaks (alias, splits) under `gen/configs/<slug>.yaml`
following oapi-codegen's config schema.

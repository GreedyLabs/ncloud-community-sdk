# ncloud-community-spec

Community-maintained, comprehensive OpenAPI 3.0 specifications for [NAVER Cloud
Platform](https://www.ncloud.com/) services, by
[GreedyLabs](https://github.com/greedylabs).
**77 services, 1,937 operations, 100% verified.**

This repo is the **single source of truth** for NCP API surface. Downstream
projects (`ncloud-community-sdk-*`, `ncloud-community-terraform`) consume it
as a git submodule, pinned to a release tag.

## Coverage

- **77 services** across Compute, Storage, Networking, Database, Media,
  Security, AI, Big Data, Developer Tools, Management, Blockchain, Migration,
  Digital Twin
- **3 environments** verified per service: 민간 (public), 금융 (financial),
  공공 (gov) — see [`docs/env-availability.md`](docs/env-availability.md)
- **NRN annotations** evidenced from NCP Resource Manager — see
  [`docs/nrn-coverage.md`](docs/nrn-coverage.md)
- **Standard error envelopes** (`4XX`/`5XX`) on every operation
- **Lint:** OpenAPI 3.0.3 validator passes 77/77

## Browse the spec

The Scalar-based interactive viewer is deployed via GitHub Pages —
see the repo description for the live URL. To preview locally:

```bash
python3 -m http.server -d . 8080
open http://localhost:8080/
```

## Use the spec

```bash
# As a git submodule
git submodule add https://github.com/greedylabs/ncloud-community-spec.git spec

# Pin to a release
git -C spec checkout v1.0.0
```

For Go SDK consumers, see [ncloud-community-sdk-go](https://github.com/greedylabs/ncloud-community-sdk-go).
For Terraform, see [ncloud-community-terraform](https://github.com/greedylabs/ncloud-community-terraform).

## Local development

```bash
make lint     # OpenAPI 3.0.3 validation, 77/77 must pass
```

This repo carries **no internal tooling** — only the spec data + viewer.
The single dependency is `openapi-spec-validator` (upstream). Audit /
quality / regression checks live in the private `ncloud-community-tools`
repo and run against this spec there.

## Contributing

Spec changes are landed via PR (manual edit) or via auto-PR opened by
`ncloud-community-tools` when its scrape pipeline detects upstream NCP
changes. Generation/maintenance tooling lives in the private repo
`ncloud-community-tools` — contributors who need to scrape or re-generate
specs should request access there.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

This is an unofficial, community-maintained project. NAVER Cloud Platform
trademarks belong to their respective owners. The canonical NCP SDK lives at
[NaverCloudPlatform/ncloud-sdk-go-v2](https://github.com/NaverCloudPlatform/ncloud-sdk-go-v2);
this project complements it with broader service coverage and an OpenAPI-first
workflow.

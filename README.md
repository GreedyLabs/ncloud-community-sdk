# ncloud-community-sdk

Community-maintained NAVER Cloud Platform stack:

| Directory | Module path | What it is |
|---|---|---|
| [`spec/`](spec) | — (YAML only) | OpenAPI 3.0.3 catalog of 78 NCP services / 1978 operations, plus a Scalar-based viewer (`index.html`) deployed via GitHub Pages — see the repo description for the live URL. |
| [`sdk/`](sdk) | `github.com/greedylabs/ncloud-community-sdk/sdk` | Go SDK generated from the spec catalog. HMAC v2 signer, AWS SigV4 for Object Storage, retry middleware, typed `APIError`, `LoadDefaultConfig` (XDG config + env precedence chain). |
| [`cli/`](cli) | `github.com/greedylabs/ncloud-community-sdk/cli` | The `ncc` command-line tool. Auto-generated cobra tree for every spec operation, sharing the SDK's auth/region/environment loader. |

The four-component split (spec / sdk / cli / tools) used to live in
separate repos but the spec ↔ sdk ↔ cli coupling was tight enough
that single-PR atomic changes saved more friction than the split was
worth. Tooling (docparse, codegen scripts, audit) stays in its own
repo at [GreedyLabs/ncloud-community-tools](https://github.com/GreedyLabs/ncloud-community-tools)
because it has a different language (Python) and lifecycle from the
publishable artifacts here.

## Repo layout

```
ncloud-community-sdk/
├── spec/                          # OpenAPI catalog
│   ├── index.html                 # Scalar viewer (root of the spec subtree)
│   ├── openapi.yaml               # x-service-files index over the 78 specs
│   ├── services/{tier}/*.yaml     # per-service specs (5 tiers)
│   └── docs/                      # spec README + audit sidecars
│
├── sdk/                           # Go module — public API
│   ├── go.mod                     # module .../sdk
│   ├── auth/                      # signers + credential providers
│   ├── services/<svc>/            # generated per-service clients
│   ├── api_error.go               # typed APIError envelope parser
│   ├── config.go                  # LoadDefaultConfig + LoadOption
│   ├── env.go                     # NewHTTPClient / NewS3HTTPClient
│   └── ...
│
├── cli/                           # Go module — `ncc` binary
│   ├── go.mod                     # module .../cli, replace ../sdk
│   ├── cmd/                       # cobra root + dispatcher + configure
│   ├── services/*.gen.go          # generated cmd registrations
│   └── tools/build_cli_ops.py     # CLI command codegen
│
├── LICENSE                        # MIT
├── README.md
└── .github/workflows/             # combined CI for all three subdirs
```

## Local development

`cli/` depends on `sdk/` via a local `replace` directive in
`cli/go.mod` so cross-module changes work without tagging:

```bash
git clone git@github.com:GreedyLabs/ncloud-community-sdk.git
cd ncloud-community-sdk

make build      # SDK + CLI
make test       # both
make lint       # spec lint + go vet
```

For consumers of the published SDK (no monorepo clone), the `replace`
in `cli/go.mod` is invisible — `go install` against a tagged module
version resolves through the standard module proxy.

### After editing a spec

The SDK and CLI are both generated FROM the spec catalog — so any
spec edit needs a regen pass before commit:

```bash
# 1. edit spec/services/<tier>/<svc>.yaml

# 2. regenerate downstream artefacts
make gen        # = gen-sdk + gen-cli

# 3. verify
make build && make test

# 4. commit spec + generated changes together
```

`make gen-sdk` runs oapi-codegen on each spec → emits
`sdk/services/<svc>/{client,endpoints}.gen.go`.
`make gen-cli` parses the SDK's generated client.gen.go (for method
signatures) + the spec (for parameter descriptions) and emits
`cli/services/<svc>.gen.go` — typed cobra commands that call the SDK
directly.

If you forget step 2: `sdk-ci` / `cli-ci` will still build the
committed files (since they're self-consistent on their own), but the
new spec ops won't appear in the SDK or CLI surface — your local
`make build && make test` immediately exposes that. Discipline +
local build is the enforcement layer; we don't add a separate CI
"regen drift" guard because it would just duplicate what `make` and
the path-filtered CI already cover.

## Versioning + release

CLI is the project's primary deliverable, so it gets the bare
`vX.Y.Z` tag at the repo root. SDK gets a prefixed `sdk/vX.Y.Z`
because Go's module proxy requires the `<subdir>/` prefix to resolve
versions for subdirectory modules. Independent release cadence:

| Tag | Triggers | Output |
|---|---|---|
| `vX.Y.Z` | `.github/workflows/cli-release.yml` → GoReleaser matrix | Binaries for linux/darwin/windows × amd64/arm64, archives + checksums uploaded to the GitHub Release |
| `sdk/vX.Y.Z` | `.github/workflows/sdk-release.yml` | GitHub Release with auto-notes; `proxy.golang.org` picks up the tag for `go get …/sdk@vX.Y.Z` |

The spec subtree is data — no Go module, no tagging. Consumers fetch
specs by commit SHA or `main` branch.

### CI overview

5 workflows, each path-filtered so unrelated changes don't trigger
unrelated checks:

| Workflow | Triggers on path | What it does |
|---|---|---|
| `spec.yml` | `spec/**` | OpenAPI lint + Pages deploy on main |
| `sdk-ci.yml` | `sdk/**`, `spec/**` | `go vet/build/test` |
| `cli-ci.yml` | `cli/**`, `sdk/**`, `spec/**` | `go vet/build/test` |
| `sdk-release.yml` | tag `sdk/v*` | validate + GitHub Release |
| `cli-release.yml` | tag `v*` | GoReleaser matrix → binaries |

The cross-component path filters (sdk-ci listening to `spec/**`, cli-ci
listening to both `spec/**` and `sdk/**`) reflect the dependency chain
— a spec edit can break SDK codegen; an SDK refactor can break CLI
dispatch — so we want the downstream component to re-test on upstream
changes.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

Unofficial, community-maintained. NAVER Cloud Platform trademarks
belong to their respective owners. The official NCP Go SDK lives at
[NaverCloudPlatform/ncloud-sdk-go-v2](https://github.com/NaverCloudPlatform/ncloud-sdk-go-v2);
the official CLI is distributed via NCP's documentation portal at
[cli.ncloud-docs.com](https://cli.ncloud-docs.com/docs/en/home).
This project complements both with broader service coverage and an
OpenAPI-first workflow.

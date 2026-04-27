# ncloud-community-cli

Community CLI for [NAVER Cloud Platform](https://www.ncloud.com/),
built on [ncloud-community-sdk-go](https://github.com/GreedyLabs/ncloud-community-sdk-go).
Binary name: **`ncc`** (the official NCP CLI is `ncloud`; we deliberately
pick a different name to avoid shadowing it on `$PATH`).

## Install

### Pre-built binaries

Releases land at
[github.com/GreedyLabs/ncloud-community-cli/releases](https://github.com/GreedyLabs/ncloud-community-cli/releases).

### `go install`

```bash
go install github.com/greedylabs/ncloud-community-cli@latest
# binary lands at $GOPATH/bin/ncloud-community-cli — rename if you'd
# rather have it on PATH as `ncc`.
```

## Configure

Run once to populate the shared config file at
`~/.config/ncloud-community/configure`:

```
$ ncc configure
NCP Access Key ID: ncp_iam_BPASKR...
NCP Secret Key [***]: ncp_iam_BPKSKR...
Default region [kr]:
Default environment [public]:
Wrote profile [DEFAULT] to /Users/me/.config/ncloud-community/configure
```

For multiple accounts, pass `--profile`:

```bash
ncc --profile gov-account configure
ncc --profile gov-account wms scenarios list
```

Or set environment variables — they take precedence over the file:

```bash
export NCLOUD_ACCESS_KEY_ID=...
export NCLOUD_SECRET_KEY=...
export NCLOUD_REGION=jp           # optional
export NCLOUD_ENVIRONMENT=gov     # optional
export NCLOUD_PROFILE=gov-account # optional
```

## Configuration precedence

Same chain as the underlying SDK:

1. Command-line flags (`--region`, `--environment`, `--profile`)
2. Environment variables (`NCLOUD_*`)
3. Shared config file (`$XDG_CONFIG_HOME/ncloud-community/configure`)
4. Defaults (region `kr` / environment `public`)

We deliberately do **not** read `$HOME/.ncloud/configure` (the
official NCP SDK's path) so the two CLIs can coexist without writing
to each other's files.

## Usage

The CLI exposes every operation in
[ncloud-community-spec](https://github.com/GreedyLabs/ncloud-community-spec)
as a cobra subcommand — 67 services / 1978 operations are auto-generated
from the OpenAPI catalog at build time.

```bash
ncc --help                              # 67-service tree
ncc <service> --help                    # operations under one service
ncc <service> <operation> --help        # parameters of one operation

# Examples
ncc wms list-monitoring-scenarios
ncc sub-account get-subject-list
ncc -o json server get-server-instance-list | jq .
ncc -o yaml object-storage list-objects --bucket-name my-bucket
```

Operations expose their OpenAPI parameters as named flags
(`--scenarioId 12345`), with `[QUERY]`/`[PATH]`/`[HEADER]` tags and
enum hints in `--help`. Mutating operations (POST/PUT/PATCH) accept
the request body via either `--body-file path/to.json` or
`--body '{"...": "..."}'`.

Output formats:

```bash
ncc -o json …   # default-friendly machine-readable (sorted keys, pretty)
ncc -o yaml …   # YAML
ncc -o table …  # falls back to JSON for now (per-service table renderers
                # are a future enhancement; the auto-generated dispatcher
                # doesn't have typed columns)
```

## How the command tree is built

Each generated file under `services/<slug>.gen.go` registers one
service with the runtime dispatcher (`cmd/registry.go`). At startup
every registered service is wired into the cobra tree, sorted
alphabetically. The dispatcher itself (`cmd/dispatch.go`) builds a
generic signed HTTP request from the operation metadata + flag values
— no per-service typed code is needed.

To regenerate from a refreshed spec catalog:

```bash
git submodule update --remote spec      # or pull the spec repo separately
make gen                                # writes services/*.gen.go
make build
```

## Build from source

```bash
git clone git@github.com:GreedyLabs/ncloud-community-cli.git
cd ncloud-community-cli
make build         # ./bin/ncc
make test          # unit tests
make install       # $GOBIN/ncc
```

## Versioning

Tagged `vMAJOR.MINOR.PATCH`. Each release pins a specific version of
`ncloud-community-sdk-go`; consumers always know which SDK version
their CLI was built against (`ncc version` prints it).

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

Unofficial, community-maintained. NAVER Cloud Platform trademarks
belong to their respective owners. The official NCP CLI is
distributed via NCP's documentation portal at
[cli.ncloud-docs.com](https://cli.ncloud-docs.com/docs/en/home)
([download page](https://cli.ncloud-docs.com/docs/en/guide-clichange));
this project complements it with broader service coverage and a Go
implementation.

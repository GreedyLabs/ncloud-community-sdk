# ncloud-community-sdk-go

Go client SDK for [NAVER Cloud Platform](https://www.ncloud.com/) APIs,
generated from [ncloud-community-spec](https://github.com/greedylabs/ncloud-community-spec)
by [GreedyLabs](https://github.com/greedylabs).

**68 services across two auth tiers.** Three NCP environments
(민간 / 금융 / 공공) selectable at construction time. Auth handled
transparently by an `http.RoundTripper` per scheme.

## Scope

This SDK generates clients for two NCP service tiers:

| Tier | Specs | Auth | Constructor |
|---|---|---|---|
| **NCP HMAC v2** | 67 ([`services/hmac-v2/`](https://github.com/greedylabs/ncloud-community-spec/tree/main/services/hmac-v2)) | NCP IAM signing (single access-key/secret) | `ncloudgo.NewHTTPClient(...)` |
| **AWS SigV4 (Object Storage)** | 1 ([`services/s3-compat/`](https://github.com/greedylabs/ncloud-community-spec/tree/main/services/s3-compat)) | AWS Signature V4 with NCP IAM keys | `ncloudgo.NewS3HTTPClient(...)` |

Same IAM credential pair is reused — NCP issues one access-key/secret
that signs both flavors. The SDK exposes them as **two distinct
constructors** because the wire-level signing differs and you should
never accidentally use the wrong signer for an endpoint.

The spec catalog documents two more tiers that are **not** generated
into this SDK because their per-app/per-product credential models can't
be unified by any SDK abstraction:

| Tier | Spec dir | Why excluded |
|---|---|---|
| Naver-branded apps | [`services/ncloud-app/`](https://github.com/greedylabs/ncloud-community-spec/tree/main/services/ncloud-app) | Each product has its own per-app/per-project secret (CLOVA Speech, OCR, Studio, Papago dect, Arc Eye, NCloud Chat, GAMEPOT, Application Maps) |
| Legacy KMS v1 | [`services/hmac-v1/`](https://github.com/greedylabs/ncloud-community-spec/tree/main/services/hmac-v1) | HMAC v1 + APIGW key (4-header hybrid). Use KMS v2 instead. |

If you do want to call those, the [`auth/`](auth) package ships
building-block transports (`ClientKeyTransport`, `BearerTransport`,
`HeadersTransport`) you can wire into a hand-rolled `*http.Client`
around an oapi-codegen client built from the relevant spec.

## Layout

```
auth/                       HMAC v2 + SigV4 signers, credential providers (env, file, chain),
                            retry transport, custom-header transports
config.go                   LoadDefaultConfig + functional options (precedence chain)
env.go                      Environment / Region types + http.Client factories
api_error.go                APIError + AsAPIError parser (8 NCP envelope shapes)
services/<svc>/client.gen.go    oapi-codegen output (generated)
services/<svc>/endpoints.gen.go ServerEndpoints + NewFromConfig (generated)
gen/                        Codegen scripts; rebuild from the pinned spec submodule
examples/                   Working end-to-end examples
spec/                       Git submodule → ncloud-community-spec
```

The `services/<svc>/` packages are public (not under `internal/`); external
consumers can import them directly.

## Install

```bash
go get github.com/greedylabs/ncloud-community-sdk-go
```

## Use

The recommended path mirrors the AWS SDK v2 pattern: load a unified
config once, then hand it to per-service `NewFromConfig` helpers.

```go
import (
    "context"

    ncloudgo "github.com/greedylabs/ncloud-community-sdk-go"
    wms "github.com/greedylabs/ncloud-community-sdk-go/services/wms"
)

func example(ctx context.Context) error {
    cfg, err := ncloudgo.LoadDefaultConfig(ctx)   // region+env+creds
    if err != nil {
        return err
    }
    client, err := wms.NewFromConfig(cfg)
    if err != nil {
        return err
    }
    resp, err := client.ListMonitoringScenariosWithResponse(ctx, nil)
    if err != nil {
        return err
    }
    _ = resp.JSON200 // typed response body
    return nil
}
```

`LoadDefaultConfig` accepts functional options — `WithRegion("jp")`,
`WithEnvironment(ncloudgo.EnvFinancial)`, `WithProfile("gov-account")`,
`WithCredentialsProvider(...)`, `WithSharedConfigPath("/etc/ncloud/configure")`.

For the lower-level `NewHTTPClient` / `NewS3HTTPClient` builders (when
you want to construct an `*http.Client` yourself, e.g. to add custom
middleware), see [`examples/`](examples).

## Configuration precedence

`LoadDefaultConfig` walks four sources, highest to lowest:

1. **Programmatic options** passed to `LoadDefaultConfig`
2. **Environment variables** — `NCLOUD_REGION`, `NCLOUD_ENVIRONMENT`,
   `NCLOUD_ACCESS_KEY_ID`, `NCLOUD_SECRET_KEY`, `NCLOUD_PROFILE`
3. **Shared config file** — INI format, looked up in order:
   - `$XDG_CONFIG_HOME/ncloud-community/configure`
   - `$HOME/.config/ncloud-community/configure`

   `$HOME/.ncloud/configure` (the official NCP Go SDK's location) is
   intentionally **not** searched, so this SDK and the official one
   can coexist on the same machine without writing to each other's
   files. Point `WithSharedConfigPath("$HOME/.ncloud/configure")` at it
   if you do want to share.
4. **Hard-coded defaults** — region `kr`, environment `public`

Example shared config file:

```ini
[DEFAULT]
ncloud_access_key_id     = ncp_iam_BPASKR...
ncloud_secret_access_key = ncp_iam_BPKSKR...
ncloud_region            = kr
ncloud_environment       = public

[gov-account]
ncloud_access_key_id     = ...
ncloud_secret_access_key = ...
ncloud_environment       = gov
```

Select a non-DEFAULT profile with `WithProfile("gov-account")` or
`NCLOUD_PROFILE=gov-account`.

## Environment selection

`ncloudgo.Config.Env` rewrites the apex domain on every request:

| Env | Apex |
|---|---|
| `EnvPublic` (민간) | `apigw.ntruss.com` |
| `EnvFinancial` (금융) | `apigw.fin-ntruss.com` |
| `EnvGov` (공공) | `apigw.gov-ntruss.com` |

Per-service availability across these environments is documented in the spec
repo's [`docs/env-availability.md`](https://github.com/greedylabs/ncloud-community-spec/blob/main/docs/env-availability.md).
Two services (`nks`, `ses2`) have region-dependent paths; their
generated `NewFromConfig` reads `cfg.Region` (uppercase NCP region
codes — `KR`, `FKR`, `JP`, `SG`, `USW`, …) and resolves the right
basePath automatically.

## Build from source

```bash
git submodule update --init --recursive   # fetch the spec submodule
make gen                                  # regenerate services/<svc>/
make test
```

## Versioning

Releases are tagged `vMAJOR.MINOR.PATCH`. Each release pins a specific
commit of `ncloud-community-spec`; consumers get a stable surface while the
spec evolves.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

This is an unofficial, community-maintained project. NAVER Cloud Platform
trademarks belong to their respective owners. The official NCP Go SDK lives
at [NaverCloudPlatform/ncloud-sdk-go-v2](https://github.com/NaverCloudPlatform/ncloud-sdk-go-v2);
this project complements it with broader service coverage and an
OpenAPI-first workflow.

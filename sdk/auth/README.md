# NCP Authentication — taxonomy

NCP exposes APIs under several distinct authentication schemes. This SDK
keeps them clearly separated so a caller can never mix credentials by
accident. Pick **one** scheme per `*http.Client` you build.

## Schemes

| Scheme | Used by | Headers | SDK type |
|---|---|---|---|
| **HMAC v2** (default) | 67 of 77 services — VPC, Server, Sub-Account, WMS, SSO, Secret Manager, etc. | `x-ncp-iam-access-key`, `x-ncp-apigw-timestamp`, `x-ncp-apigw-signature-v2` | `auth.Provider` (e.g. `auth.DefaultChain()`) |
| **AWS SigV4** | NCP Object Storage (`*.ncloudstorage.com`) | `Authorization: AWS4-HMAC-SHA256 ...`, `X-Amz-Date`, `X-Amz-Content-Sha256` | `auth.SigV4Signer` / `auth.SigV4Transport` (built into `ncloudgo.NewS3HTTPClient`) |

### SigV4 streaming uploads (UNSIGNED-PAYLOAD)

Default SigV4 signing reads the entire request body to compute the
SHA-256 it puts on `X-Amz-Content-Sha256`. That works for objects you
can hold in memory but defeats streaming uploads from non-seekable
sources (stdin, network pipes, large files you don't want to buffer).

Pre-set the header to `UNSIGNED-PAYLOAD` and the signer uses the
literal value instead of touching the body — TLS still guarantees
on-wire integrity. Use `auth.UnsignedPayloadEditor` as a
`RequestEditorFn`:

```go
hc, _ := ncloudgo.NewS3HTTPClient(ncloudgo.S3Config{})
c, _  := object_storage.NewClientWithResponses(
    "https://kr.object.ncloudstorage.com",
    object_storage.WithHTTPClient(hc),
)

// Body is a 50 GB pipe / network stream — never buffered in memory.
resp, _ := c.PutObjectWithBodyWithResponse(ctx, "my-bucket", "huge.bin",
    &object_storage.PutObjectParams{},
    "application/octet-stream", veryLargeReader,
    auth.UnsignedPayloadEditor)
```

This is the same approach the official AWS SDK takes by default for S3
PutObject. Verified live against NCP Object Storage. For workloads that
need resumability or parallelism on top of streaming, prefer the
multipart helper at
[`object_storage.UploadLargeObject`](../services/object_storage/multipart.go)
instead.
| **APIGW Client Key** | Application Maps, Naver/Papago `dect` | `X-NCP-APIGW-API-KEY-ID`, `X-NCP-APIGW-API-KEY` | `auth.ClientKeyProvider` |
| **Bearer Token** | CLOVA Studio | `Authorization: Bearer <token>` (+ `X-NCP-CLOVASTUDIO-REQUEST-ID`) | `auth.BearerProvider` |
| **Custom single-secret** | CLOVA Speech, CLOVA OCR, Arc Eye, NCloud Chat | varies — `X-CLOVASPEECH-API-KEY`, `X-OCR-SECRET`, `X-ARCEYE-SECRET`, `x-api-key`+`x-project-id` | `auth.HeadersProvider` |
| **HMAC v1 + APIGW key** (legacy) | KMS v1 only | `x-ncp-iam-access-key`, `x-ncp-apigw-timestamp`, `x-ncp-apigw-signature-v1`, `x-ncp-apigw-api-key` | not implemented — use plain `*http.Request` and add headers manually for now |

The *exact* scheme each spec uses is encoded in its OpenAPI
`components.securitySchemes` and top-level `security:` block. If you
build a custom client, look there first.

## Environment variables

There are only **three** distinct credential domains in NCP — every header
above is a presentation of one of them:

| Domain | Env vars | Issued in | Used by |
|---|---|---|---|
| **IAM** (HMAC v2) | `NCLOUD_ACCESS_KEY_ID` + `NCLOUD_SECRET_KEY` | IAM console | ~65 services |
| **APIGW API Key** | `NCLOUD_API_KEY_ID` + `NCLOUD_API_KEY` | API Gateway → API Key 생성 | Application Maps, Papago dect (header pair); CLOVA Speech, CLOVA OCR (Secret only, different header) |
| **Per-product** | varies — `NCLOUD_BEARER_TOKEN`, `NCLOUD_ARC_EYE_SECRET`, `NCLOUD_CHAT_API_KEY` + `NCLOUD_CHAT_PROJECT_ID`, … | each product's own console | CLOVA Studio (Bearer), Arc Eye, NCloud Chat |

Key insight: **`NCLOUD_API_KEY` powers both the standard ClientKey pair AND
the CLOVA-style "Secret only, in a custom header" calls** (the docs spell
out "{앱 등록 시 발급받은 Secret Key}" for both). One env var, multiple header
shapes — pick the right SDK transport for the product.

The Client ID portion (`NCLOUD_API_KEY_ID`) is only consumed by products
that send the standard `X-NCP-APIGW-API-KEY-ID` header. CLOVA Speech /
OCR don't need it because the app is identified by URL/subdomain.

A copy-pasteable `.env.example` lives at the repo root.

`NCLOUD_API_KEY_ID/NCLOUD_API_KEY` is **deliberately distinct** from
`NCLOUD_ACCESS_KEY_ID/NCLOUD_SECRET_KEY` so a single process can hold
both kinds of credential without collision. The IAM keys come from
the IAM console; the APIGW keys come from registering an "API Key"
against an app in the API Gateway console.

## Picking the right scheme — examples

### HMAC v2 (default)

```go
hc, err := ncloudgo.NewHTTPClient(ncloudgo.Config{
    Env:   ncloudgo.EnvPublic,           // optional, defaults to public
    Creds: auth.DefaultChain(),          // optional, auto-applied when nothing set
})
// Hand to any oapi-codegen client …
c, _ := wms.NewClientWithResponses(
    "https://wms.apigw.ntruss.com/api/v1",
    wms.WithHTTPClient(hc),
)
```

### APIGW Client Key (Papago `dect`, Application Maps)

```go
hc, err := ncloudgo.NewHTTPClient(ncloudgo.Config{
    ClientKey: auth.EnvClientKey{},     // reads NCLOUD_API_KEY_ID / NCLOUD_API_KEY
})
c, _ := naver.NewClientWithResponses(
    "https://papago.apigw.ntruss.com",
    naver.WithHTTPClient(hc),
)
```

You can also pass a static pair if env-var loading is undesirable:

```go
hc, _ := ncloudgo.NewHTTPClient(ncloudgo.Config{
    ClientKey: auth.StaticClientKey(auth.ClientKey{
        ClientID:     "abcd1234",
        ClientSecret: "<secret>",
    }),
})
```

### Bearer Token (CLOVA Studio)

```go
hc, err := ncloudgo.NewHTTPClient(ncloudgo.Config{
    Bearer: auth.EnvBearer{},           // reads NCLOUD_BEARER_TOKEN
    BearerExtraHeaders: map[string]string{
        // CLOVA Studio also wants a per-request ID for tracing.
        "X-NCP-CLOVASTUDIO-REQUEST-ID": "smoke-001",
    },
})
c, _ := clova_studio.NewClientWithResponses(
    "https://clovastudio.stream.ntruss.com",
    clova_studio.WithHTTPClient(hc),
)
```

### CLOVA Speech / CLOVA OCR — APIGW Secret in a different header

These two products are issued via API Gateway just like ClientKey, but
they only want the Secret value (no Client ID), under their own header
name. So they re-use `NCLOUD_API_KEY` from block 2 of `.env.example`:

```go
// CLOVA OCR. Uses a per-customer subdomain — set NCLOUD_OCR_SUBDOMAIN
// to the value the console gave you (the bit before .apigw.ntruss.com).
hc, err := ncloudgo.NewHTTPClient(ncloudgo.Config{
    APIKeySecretOnly: &auth.APIKeySecretOnlyTransport{
        HeaderName: "X-OCR-SECRET",
        Keys:       auth.EnvClientKey{},   // reads NCLOUD_API_KEY (Secret)
    },
})
c, _ := ocr.NewClientWithResponses(
    "https://"+os.Getenv("NCLOUD_OCR_SUBDOMAIN")+".apigw.ntruss.com",
    ocr.WithHTTPClient(hc),
)
```

CLOVA Speech is identical, just with `HeaderName: "X-CLOVASPEECH-API-KEY"`.

### Per-product secrets (Arc Eye, NCloud Chat)

These are NOT issued via API Gateway — each product has its own
console-level credential, so they need their own env vars:

```go
// NCloud Chat — two headers from the project's own console.
hc, _ := ncloudgo.NewHTTPClient(ncloudgo.Config{
    CustomHeaders: auth.EnvHeaders{Mapping: map[string]string{
        "x-api-key":    "NCLOUD_CHAT_API_KEY",
        "x-project-id": "NCLOUD_CHAT_PROJECT_ID",
    }},
})

// Arc Eye — single header, plus a per-customer InvokeURL.
hc, _ := ncloudgo.NewHTTPClient(ncloudgo.Config{
    CustomHeaders: auth.EnvHeaders{Mapping: map[string]string{
        "X-ARCEYE-SECRET": "NCLOUD_ARC_EYE_SECRET",
    }},
})
c, _ := arc_eye.NewClientWithResponses(
    os.Getenv("NCLOUD_ARC_EYE_INVOKE_URL"),
    arc_eye.WithHTTPClient(hc),
)
```

## Mixing is rejected

`NewHTTPClient` returns an error if more than one of `ClientKey`,
`Bearer`, or `CustomHeaders` is set. NCP services accept exactly one
scheme each, so layering two would only produce requests no real
product would recognise.

## Footnote — KMS v1 hybrid

The legacy KMS v1 endpoints (`kms.apigw.ntruss.com/keys/v1/...`) want
**four** headers: the IAM trio *and* an APIGW Client Key alongside.
The SDK does not yet expose a dedicated transport for this — wire it
up by stacking `auth.Transport` (HMAC v2 — but compute v1 manually) on
top of `auth.ClientKeyTransport`. Most consumers should just use KMS
v2 (the `kms2` spec, which lives on `ocapi.ncloud.com` and uses normal
HMAC v2).

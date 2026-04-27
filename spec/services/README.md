# services/ — NCP API spec catalog

78 OpenAPI 3.0 specs grouped by **authentication tier**. Each subdirectory
is one credential domain — pick the directory that matches the auth
scheme you can actually wire on the client side.

| Directory | Specs | Auth | Generated into Go SDK? |
|---|---:|---|---|
| [`hmac-v2/`](./hmac-v2) | 66 | NCP IAM HMAC v2 (single access-key/secret pair) | **Yes** — see `ncloudgo.NewHTTPClient` |
| [`s3-compat/`](./s3-compat) | 1 | AWS Signature Version 4 (S3 wire protocol) | **Yes** — see `ncloudgo.NewS3HTTPClient` |
| [`ncloud-app/`](./ncloud-app) | 9 | Per-app/per-project secrets (Naver-branded apps) | No |
| [`hmac-v1/`](./hmac-v1) | 1 | Legacy HMAC v1 + APIGW key (KMS v1 only) | No |
| [`swift-compat/`](./swift-compat) | 1 | OpenStack Swift token (Archive Storage) | No |

## Why the split

NCP groups all 78 services under one console and one billing surface,
but the authentication models below them are NOT unified. Three of the
five tiers use credentials that are issued **per app or per project** —
there is no single "NCP login" that covers them all. A single SDK
abstraction over five different credential domains would either lie
about unification or expose so much auth surface that it stops being
an SDK and starts being a transport zoo.

The Go SDK (`ncloud-community-sdk-go`) therefore restricts itself to
the two tiers where one IAM credential pair really does work:
**hmac-v2** (66 services, NCP HMAC v2 signing) and **s3-compat**
(1 service, AWS SigV4 signing — same IAM keys, different canonicalisation).
The other three directories are kept here as a community catalog —
anyone can run `oapi-codegen` over them and wire their own client. The
SDK's `auth/` package ships building-block RoundTrippers
(`auth.ClientKeyTransport`, `auth.BearerTransport`,
`auth.HeadersTransport`) that fit straight into a hand-rolled client.

---

## hmac-v2/ — NCloud Platform (HMAC v2)

The 66 specs in this directory describe NCP services that share a
single authentication model: NCP IAM HMAC v2 signing. One IAM
access-key/secret pair (issued in the IAM console) authenticates calls
to every service in this directory.

Headers per request:
- `x-ncp-iam-access-key`
- `x-ncp-apigw-timestamp`
- `x-ncp-apigw-signature-v2`

If you can sign a request with HMAC v2 and you have an IAM key pair,
every spec here is callable through the same `*http.Client`.

## s3-compat/ — AWS S3 SigV4 protocol (Object Storage)

`object-storage.yaml` describes NCP Object Storage, which is wire-compatible
with Amazon S3 — including the auth scheme. The endpoint at
`{regionCode}.object.ncloudstorage.com` accepts only AWS SigV4 signatures.

Headers per request:
- `Authorization: AWS4-HMAC-SHA256 Credential=…/<scope>, SignedHeaders=…, Signature=…`
- `X-Amz-Date`
- `X-Amz-Content-Sha256`

Live verification 2026-04-26: a request signed with NCP HMAC v2 against
`https://kr.object.ncloudstorage.com/` is rejected as
`AccessForbidden — GET Service not allowed for anonymous users`. The
response envelope itself is in S3 XML format, not the NCP standard
envelope. Only SigV4 is recognised.

The same NCP IAM access-key/secret pair is used as the SigV4 signing
material — only the canonicalisation differs from HMAC v2.

## ncloud-app/ — Naver-branded apps

These products live under the NCP umbrella (billing, console listing)
but are essentially **separate apps with their own credential domains**.
There is no IAM tie-in: each user registers an app in the product's
own console (or in API Gateway), receives a per-app secret, and
supplies that secret on every call.

Because every product issues its own credentials, the SDK cannot offer
a unified auth chain for them — calling N of these products requires
N distinct secrets, no matter how the SDK is structured. So the core
SDK does **not** generate clients for this tier.

The specs live here as a community catalog. To call any of them, run
`oapi-codegen` over the spec yourself and wire your own `*http.Client`
that injects the right header.

| Spec | Auth | Header(s) | Issued in |
|---|---|---|---|
| `application-maps.yaml` | APIGW Client Key | `X-NCP-APIGW-API-KEY-ID`, `X-NCP-APIGW-API-KEY` | API Gateway → API Key 생성 |
| `arc-eye.yaml` | Per-product secret | `X-ARCEYE-SECRET` | Console → Digital Twin → ARC eye → Visual Localization → Data Management |
| `chatbot.yaml` | Per-domain HMAC | `X-NCP-CHATBOT-SIGNATURE` (Service ID + Secret Key issued per chatbot domain) | CLOVA Chatbot console (per domain) |
| `clova-speech.yaml` | APIGW Secret only | `X-CLOVASPEECH-API-KEY` | API Gateway → API Key 생성 |
| `clova-studio.yaml` | Bearer token | `Authorization: Bearer …`, `X-NCP-CLOVASTUDIO-REQUEST-ID` | CLOVA Studio console |
| `gamepot.yaml` | Per-project secret | `x-api-key` | GAMEPOT dashboard (per project) |
| `naver.yaml` | APIGW Client Key | `X-NCP-APIGW-API-KEY-ID`, `X-NCP-APIGW-API-KEY` | API Gateway → API Key 생성 |
| `ncloud-chat.yaml` | Per-project secret | `x-api-key`, `x-project-id` | NCloud Chat console (per project) |
| `ocr.yaml` | APIGW Secret only | `X-OCR-SECRET` | API Gateway → API Key 생성 (also per-customer subdomain) |

> **Note** on `chatbot.yaml`: the public NCP doc pages for CLOVA Chatbot
> are JavaScript-rendered (Document360 SPA), so docparse can't extract
> their operation tables from the static HTML. The spec is currently a
> stub (`paths: {}`). The three documented operations
> (Open / Send / GetPersistentMenu) need to be transcribed manually.

## hmac-v1/ — Legacy KMS v1

The single spec here (`kms.yaml`) describes the first-generation Key
Management Service. It is kept because real customers still operate
v1 keys, but the auth model is incompatible with the rest of the
platform:

- IAM access key (HMAC v1 signature, NOT v2)
- AND an APIGW Client Key alongside (4 headers total)

The newer KMS v2 (see `hmac-v2/kms2.yaml`) is plain HMAC v2 and is
what new code should target.

The Go SDK does not generate a client for this spec. To call it,
derive your own client from this spec and sign with HMAC v1 (different
canonicalisation than v2 — the canonical-string ordering differs).

## swift-compat/ — OpenStack Swift (Archive Storage)

`archive-storage.yaml` describes NCP **Archive Storage**, which is an
OpenStack Swift implementation. It is wholly distinct from Object
Storage — different wire protocol, different auth.

Two-step Keystone-style flow:

1. Obtain an `X-Auth-Token` from the **identity endpoint**:
   `https://kr.archive.ncloudstorage.com:5000`
2. Send the token on the `X-Auth-Token` request header to the
   **service endpoint**: `https://kr.archive.ncloudstorage.com`

Tokens expire — refresh as needed. For SubAccounts, an alternative
route is provided through the standard NCP API Gateway:
`https://archive-storage-external-api.apigw.ntruss.com`.

Neither NCP HMAC v2 nor AWS SigV4 will work against this endpoint —
it expects Swift's bespoke token-based auth. The Go SDK does not
generate a client for this spec; use any OpenStack Swift client
library, or roll your own thin client over the spec.

# Viewer

The repo root is itself the viewer — `index.html` at the repo root
embeds [Scalar API Reference](https://github.com/scalar/scalar) and
renders every spec in this catalog (78 services across 5 auth tiers)
with a sidebar source picker.

GitHub Pages serves the spec/ subtree at the project's Pages URL —
see the repo description for the canonical link.

## Local preview

```bash
python3 -m http.server -d . 8080
open http://localhost:8080/
```

Must be served over HTTP (not `file://`) so the YAML fetch for
`./openapi.yaml` and the per-service spec files works.

## Hosting

Deployed via GitHub Pages on `main` branch — see
`.github/workflows/ci.yml` `deploy-viewer` job. The job uploads the
entire repository as the Pages artifact, so `index.html` (root) and
the spec YAMLs sit at the same level the runtime fetches expect.

## Why Scalar

- Native multi-source support: pass an array of `{url, title, slug}` and
  Scalar renders a sidebar picker. We don't have to build our own
  switcher.
- Single CDN script — no React/Vite/Node build pipeline; works as a
  vanilla static page on GitHub Pages.
- Tier suffix on titles for the 12 non-default specs (`object-storage · S3`,
  `archive-storage · Swift`, `application-maps · App`, `kms · HMAC v1`)
  flags the auth model where it differs without polluting the 66 hmac-v2
  entries.
- Modern visual design + dark mode out of the box, fits a
  community-SDK presentation tone.

The previous viewer used Stoplight Elements, which only handles a
single spec per `<elements-api>` and required a hand-rolled JS picker.
Switching to Scalar removes that custom code and fixes a regression
where the picker stopped working after specs were moved into per-tier
subdirectories.

Reference: [scalar/scalar](https://github.com/scalar/scalar),
[live demo](https://docs.scalar.com/swagger-editor).

## Spec catalog source

The viewer reads its source list at runtime by parsing
`./openapi.yaml`'s `x-service-files` block. To add or remove a spec,
edit that block — the viewer follows automatically on next page load.

## "Try It" panel — disabled

`hideClientButton` and `hideTestRequestButton` are both set in the Scalar
config. Two reasons:

1. **NCP API Gateway has no CORS headers.** A static-hosted viewer
   cannot call `*.apigw.ntruss.com` directly — the browser blocks all
   responses regardless of payload.
2. **NCP authentication is HMAC v2 signing** (per-request
   `x-ncp-apigw-signature-v2` header derived from
   method+path+timestamp+secret). Even with CORS, signature injection
   from a static page would mean either shipping the secret to the
   browser or running a sign-and-forward proxy — neither acceptable
   for a public viewer.

For interactive testing, use the official console API Explorer, `curl`
with locally-computed HMAC v2, or the Go SDK
(`ncloud-community-sdk-go`) which handles signing transparently.

We deliberately do **not** override Scalar's default code samples or
substitute env-var-style placeholder values into them. Scalar's
out-of-box rendering is the standard and any custom substitution we
add ends up either misleading (placeholder env names that aren't
actually substituted) or stale (precomputed timestamps/signatures that
expire 5 minutes after page load).

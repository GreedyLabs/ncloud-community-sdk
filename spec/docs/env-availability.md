# Environment Availability Matrix

NAVER Cloud Platform (NCP) operates three independent environments — 민간
(public), 금융 (financial), 공공 (gov) — each backed by a separate API
documentation portal. Not every service is offered in every environment.

This document records, per service, which of the three environments expose
the API. The data is derived from probing the official portals:

- `https://api.ncloud-docs.com/docs/<slug>` (public)
- `https://api-fin.ncloud-docs.com/docs/<slug>` (financial)
- `https://api-gov.ncloud-docs.com/docs/<slug>` (gov)

A service is considered **available** in an environment when at least one of
its operations has a documented page on that environment's portal. The same
information is denormalized into each spec as `info.x-ncloud-availability`.

Machine-readable source: [`env-availability.json`](./env-availability.json).

---

## Summary

| Coverage | Count | Share |
|---|---:|---:|
| All three environments (PFG) | 38 | 49.4% |
| Public + gov only (P·G) | 12 | 15.6% |
| Public + financial only (PF·) | 4 | 5.2% |
| Public only (P··) | 23 | 29.9% |
| **Total tracked services** | **77** | **100%** |

Per-environment totals:

| Environment | Supported / 77 |
|---|---:|
| 민간 (public) | 77 |
| 금융 (financial) | 42 |
| 공공 (gov) | 50 |

## Service breakdown

### Available in all three environments — 38

`api-gateway`, `auto-scaling`, `cache`, `cdss`, `cloud-activity-tracer`,
`cloud-functions`, `cloud-insight`, `cloud-log-analytics`,
`cloud-outbound-mailer`, `file-safer`, `global-dns`, `kms`, `load-balancer`,
`mssql`, `mysql`, `nas`, `naver`, `object-storage`, `ocr`, `papago`,
`platform`, `resource-manager`, `sens`, `server`, `ses`, `ses2`,
`source-build`, `source-commit`, `source-deploy`, `source-pipeline`, `sso`,
`sts`, `sub-account`, `vod-station`, `vpc`, `web-security-checker`,
`webshell-behavior-detector`, `wms`

### Public + gov, no financial — 12

`cloud-search`, `clova-studio`, `drm`, `geo-cidr`, `global-traffic-manager`,
`hadoop`, `kms2`, `mongodb`, `postgresql`, `private-ca`, `security-monitoring`,
`vpe`

### Public + financial, no gov — 4

`chatbot`, `clova-speech`, `live-station`, `media-connect-center`

### Public only — 23

`ai-tems`, `application-maps`, `arc-eye`, `blockchain`, `cdn`,
`certificate-manager`, `cloud-advisor`, `data-box-frame`, `data-catalog`,
`data-fence`, `data-flow`, `data-forest`, `data-query`, `data-stream`,
`edge`, `gamepot`, `geo-location`, `maiu`, `ncloud-chat`, `nclue`, `nks`,
`organization`, `secret-manager`

## Notes for SDK / Terraform consumers

When generating code from the specs, branch the client construction by
environment using `info.x-ncloud-availability`. Calling an unsupported
environment will yield HTTP 404 / DNS resolution failure rather than a
typed error. The Go SDK's `ncloudgo.Config{Env: ...}` selects the apex
domain (`ntruss.com` / `fin-ntruss.com` / `gov-ntruss.com`) accordingly.

# NRN Coverage

NCP Resource Names (NRN) are NAVER Cloud Platform's canonical resource
identifiers, used in IAM policies, the Resource Manager, audit logs, and
SDK responses. Their format is:

```
nrn:<domainCode>:<productName>:<regionCode>:<memberNo>:<resourceType>/<resourceId>
```

This document records, for each service in this repo, which `productName`
its resources use and which `resourceType` values exist. The mapping is
sourced from NCP's [Resource Manager concept page][rmgr] and denormalized
into each spec as `info.x-ncloud-nrn`.

The full canonical table — every NCP product code with its resource types
— lives in [`nrn-authoritative.yaml`](./nrn-authoritative.yaml).

[rmgr]: https://guide.ncloud-docs.com/docs/management-rmgr-1-1-2

---

## Policy

Only services explicitly listed in the Resource Manager documentation
receive an evidenced NRN annotation. Services not yet registered there
carry an `info.x-ncloud-nrn-todo: true` marker instead, with no
`resource_types` populated. This avoids guessing identifiers that NCP has
not formally defined.

## Coverage

| Status | Count | Share |
|---|---:|---:|
| Evidenced (mapped to a Resource Manager `productName`) | 61 | 79% |
| Pending (no Resource Manager entry yet) | 16 | 21% |
| **Total** | **77** | **100%** |

## Evidenced — 61

Each row maps a spec slug (the filename under `services/`) to its
`productName` from the Resource Manager table.

| Slug | productName |
|---|---|
| `api-gateway` | APIGateway |
| `arc-eye` | ARCeye |
| `auto-scaling` | VPCAutoScaling |
| `blockchain` | BlockchainService |
| `cache` | VPCCloudDBforRedis |
| `cdn` | GCDN |
| `certificate-manager` | CertificateManager |
| `chatbot` | Chatbot |
| `cloud-activity-tracer` | CloudActivityTracer |
| `cloud-advisor` | CloudAdvisor |
| `cloud-functions` | VPCCloudFunctions |
| `cloud-insight` | CloudInsight |
| `cloud-log-analytics` | CloudLogAnalytics |
| `cloud-outbound-mailer` | CloudOutboundMailer |
| `cloud-search` | CloudSearch |
| `clova-speech` | CLOVASpeech |
| `clova-studio` | CLOVAStudio |
| `data-box-frame` | CloudDataBox |
| `data-catalog` | DataCatalog |
| `data-flow` | DataFlow |
| `data-forest` | DataForest |
| `data-query` | DataQuery |
| `data-stream` | CloudDataStreamingService |
| `file-safer` | FileSafer |
| `gamepot` | GAMEPOT |
| `geo-location` | GeoLocation |
| `global-dns` | GlobalDNS |
| `hadoop` | VPCCloudHadoop |
| `kms` | KMS |
| `kms2` | KMS |
| `live-station` | LiveStation |
| `load-balancer` | VPCLoadBalancer |
| `media-connect-center` | MediaConnectCenter |
| `mongodb` | VPCCloudDBforMongoDB |
| `mssql` | VPCCloudDBforMSSQL |
| `mysql` | VPCCloudDBforMySQL |
| `nas` | VPCNAS |
| `ncloud-chat` | NcloudChat |
| `nks` | VPCKubernetesService |
| `object-storage` | ObjectStorage |
| `ocr` | OCR |
| `organization` | Organization |
| `postgresql` | VPCCloudDBforPostgreSQL |
| `private-ca` | PrivateCA |
| `resource-manager` | ResourceManager |
| `security-monitoring` | VPCSecurityMonitoring |
| `sens` | SENS |
| `server` | VPCServer |
| `ses` | VPCSearchEngine |
| `ses2` | VPCSearchEngine |
| `source-build` | SourceBuild |
| `source-commit` | SourceCommit |
| `source-deploy` | VPCSourceDeploy |
| `source-pipeline` | VPCSourcePipeline |
| `sso` | SSO |
| `sub-account` | IAM |
| `vod-station` | VODStation |
| `vpc` | VPC |
| `web-security-checker` | WebSecurityChecker |
| `webshell-behavior-detector` | WebshellBehaviorDetector |
| `wms` | WMS |

## Pending — 16

The following services exist in this repo but are not yet registered in
the Resource Manager Resource Type table. Their specs carry
`info.x-ncloud-nrn-todo: true` and consumers should treat their NRN as
runtime-discoverable rather than statically resolvable.

`ai-tems`, `application-maps`, `cdss`, `data-fence`, `drm`, `edge`,
`geo-cidr`, `global-traffic-manager`, `maiu`, `naver`, `nclue`, `papago`,
`platform`, `secret-manager`, `sts`, `vpe`

When NCP registers any of these in the Resource Manager, the entry will
move from this list into the evidenced table above.

## Notes for SDK / IAM-policy consumers

- The `productName` is what appears in the third NRN segment, e.g.
  `nrn:PUB:VPCServer:KR:1234:Server/567`.
- For services that share a productName across Classic and VPC variants
  (e.g. `kms` and `kms2` both → `KMS`), the Resource Manager treats them
  as a single product.
- Some specs span multiple productNames (e.g. `sub-account` covers IAM
  primary + occasional ExternalAccess references). When that happens, the
  `info.x-ncloud-nrn` block lists `additional_products` alongside the
  primary `productName`.

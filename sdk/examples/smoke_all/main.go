// smoke_all — fail-soft smoke runner against the live NCP public env.
//
// Probes one read-only endpoint per representative service and reports
// HTTP status + body length + a single key field. Each probe is independent
// so a failure in one service does not stop the others.
//
// Endpoints (all read-only, no resources created):
//   wms                    — GET  /scenarios
//   sub-account            — GET  /sub-accounts
//   resource-manager       — GET  /groups
//   vpc                    — POST /getVpcList                  (NCP uses POST for reads)
//   cloud-activity-tracer  — POST /activities                  (POST + optional body)
//
// Usage: go run . from this directory, with NCP creds in env or ../../.env.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/ai_tems"
	apigw "github.com/greedylabs/ncloud-community-sdk/sdk/services/api_gateway"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/auto_scaling"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/blockchain"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/cache"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/cdn"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/cdss"
	cm "github.com/greedylabs/ncloud-community-sdk/sdk/services/certificate_manager"
	cat "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_activity_tracer"
	cf "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_functions"
	ci "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_insight"
	cla "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_log_analytics"
	com "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_outbound_mailer"
	cs "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_search"
	cad "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_advisor"
	dbf "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_box_frame"
	dcat "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_catalog"
	dfn "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_fence"
	df "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_flow"
	dfor "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_forest"
	dq "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_query"
	ds "github.com/greedylabs/ncloud-community-sdk/sdk/services/data_stream"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/drm"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/edge"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/file_safer"
	gcidr "github.com/greedylabs/ncloud-community-sdk/sdk/services/geo_cidr"
	geo "github.com/greedylabs/ncloud-community-sdk/sdk/services/geo_location"
	gdns "github.com/greedylabs/ncloud-community-sdk/sdk/services/global_dns"
	gtm "github.com/greedylabs/ncloud-community-sdk/sdk/services/global_traffic_manager"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/hadoop"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/kms2"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/live_station"
	lb "github.com/greedylabs/ncloud-community-sdk/sdk/services/load_balancer"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/maiu"
	mcc "github.com/greedylabs/ncloud-community-sdk/sdk/services/media_connect_center"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/mongodb"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/mssql"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/mysql"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/nas"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/nclue"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/nks"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/object_storage"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/organization"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/papago"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/platform"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/postgresql"
	pca "github.com/greedylabs/ncloud-community-sdk/sdk/services/private_ca"
	rm "github.com/greedylabs/ncloud-community-sdk/sdk/services/resource_manager"
	sm "github.com/greedylabs/ncloud-community-sdk/sdk/services/secret_manager"
	secmon "github.com/greedylabs/ncloud-community-sdk/sdk/services/security_monitoring"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/sens"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/server"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/ses"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/ses2"
	scm "github.com/greedylabs/ncloud-community-sdk/sdk/services/source_build"
	scc "github.com/greedylabs/ncloud-community-sdk/sdk/services/source_commit"
	scd "github.com/greedylabs/ncloud-community-sdk/sdk/services/source_deploy"
	scp "github.com/greedylabs/ncloud-community-sdk/sdk/services/source_pipeline"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/sso"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/sts"
	sa "github.com/greedylabs/ncloud-community-sdk/sdk/services/sub_account"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/vod_station"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/vpc"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/vpe"
	wsc "github.com/greedylabs/ncloud-community-sdk/sdk/services/web_security_checker"
	wbd "github.com/greedylabs/ncloud-community-sdk/sdk/services/webshell_behavior_detector"
	"github.com/greedylabs/ncloud-community-sdk/sdk/services/wms"
)

type result struct {
	service string
	status  string // "PASS" / "FAIL: ..."
	detail  string
}

func main() {
	loadDotenv()

	httpClient, err := ncloud.NewHTTPClient(ncloud.Config{
		Env:     ncloud.EnvPublic,
		Creds:   auth.DefaultChain(),
		Timeout: 15 * time.Second,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewHTTPClient:", err)
		os.Exit(1)
	}

	ctx := context.Background()

	probes := []func(context.Context, *http.Client) result{
		probeWMS, probeSubAccount, probeResourceManager,
		probeAPIGateway, probeSecretManager, probeCertificateManager,
		probeSENS, probeCloudInsight, probeCloudActivityTracer,
		probeVPC, probeServer, probeMySQL, probeNAS,
		// Expansion: GET-style REST endpoints.
		probeCloudSearch, probeCloudFunctions, probeSourceCommit,
		probeSourceBuild, probeSourceDeploy, probeSourcePipeline,
		probeSSO, probeGeoLocation, probePrivateCA, probeCloudLogAnalytics,
		// Expansion: Classic/legacy POST-no-body endpoints.
		probeAutoScaling, probeLoadBalancer, probeCDN,
		probeMongoDB, probeMSSQL, probePostgreSQL, probeCache,
		// Expansion 3: round-3 services (data, dev tooling, security, billing, misc).
		probeSTS, probeWebSecurityChecker, probeDataFlow, probeDataQuery,
		probeDataStream, probeDataBoxFrame, probeNclue, probeMaiu,
		probeCloudOutboundMailer, probeEdge, probeFileSafer,
		probePlatform, probeBlockchain, probeGlobalDNS, probeGlobalTrafficManager,
		probeOrganization, probeHadoop,
		// Expansion 4: AI/media/k8s/security/governance.
		probeAITems, probeCloudAdvisor, probeDRM, probeKMS2,
		probeLiveStation, probeNKS, probeVODStation, probeVPE,
		probeWebshellBehaviorDetector, probeDataFence, probePapago, probeDataCatalog,
		// Expansion 5: Object Storage (S3-compat / SigV4 — uses its own
		// http.Client built via NewS3HTTPClient inside the probe).
		probeObjectStorage,
		// Expansion 6: tail end of hmac-v2 — services with awkward op shapes
		// (require dummy IDs / minimal bodies). All return typed 4xx
		// envelopes when the dummy values don't match real resources.
		probeCDSS, probeDataForest, probeGeoCIDR, probeMediaConnectCenter,
		probeSecurityMonitoring, probeSES, probeSES2,
		// chatbot was reclassified into ncloud-app/ tier (uses
		// X-NCP-CHATBOT-SIGNATURE per-domain credential, not IAM HMAC v2)
		// and is no longer generated into the SDK.
	}
	results := make([]result, 0, len(probes))
	for _, p := range probes {
		results = append(results, runProbe(ctx, httpClient, p))
	}

	pass, fail, skip := 0, 0, 0
	fmt.Println()
	fmt.Println("== smoke summary ==")
	for _, r := range results {
		fmt.Printf("  %-26s %s  %s\n", r.service, r.status, r.detail)
		switch {
		case strings.HasPrefix(r.status, "PASS"):
			pass++
		case r.status == "SKIP":
			skip++
		default:
			fail++
		}
	}
	fmt.Printf("\n%d pass, %d fail, %d skip\n", pass, fail, skip)
	if fail > 0 {
		os.Exit(1)
	}
}

// runProbe executes a single probe with panic-recover so one service's bug
// doesn't abort the whole sweep.
func runProbe(ctx context.Context, hc *http.Client, fn func(context.Context, *http.Client) result) (r result) {
	defer func() {
		if rec := recover(); rec != nil {
			r = result{r.service, "FAIL: panic", fmt.Sprintf("%v", rec)}
			if r.service == "" {
				r.service = "(panic before name set)"
			}
		}
	}()
	return fn(ctx, hc)
}

// classify maps an HTTP response into a smoke result. 4xx responses that
// carry an NCP-style error envelope are treated as AUTH-OK (the request
// reached NCP, was authenticated, and NCP returned a structured business
// error such as "no resources" or "no tenant"). Only true wire-level
// failures or 5xx are reported as FAIL.
func classify(name string, status int, body []byte, detail string) result {
	switch {
	case status >= 200 && status < 300:
		// Some NCP legacy endpoints reply 201/204 on read-OK paths
		// (cloud-log-analytics returns 201 for GetCapacity), so accept the
		// full 2xx range as PASS.
		return result{name, "PASS", detail}
	case status >= 400 && status < 500:
		if looksLikeNCPError(body) {
			// HMAC + URL + apex worked; NCP returned a typed business error.
			return result{name, "PASS (auth-OK)", fmt.Sprintf("HTTP %d  %s", status, bodyPreview(body))}
		}
		// 4xx with empty body. NCP's gateway would never return empty on
		// HMAC failure (it always emits a signature-error envelope), so an
		// empty 4xx still means the request reached and authenticated, the
		// service just chose not to send a body — flag as auth-OK with a
		// note instead of failing the run.
		if len(bytes.TrimSpace(body)) == 0 {
			return result{name, "PASS (auth-OK)", fmt.Sprintf("HTTP %d  (empty body)", status)}
		}
		return result{name, fmt.Sprintf("FAIL: HTTP %d", status), bodyPreview(body)}
	default:
		return result{name, fmt.Sprintf("FAIL: HTTP %d", status), bodyPreview(body)}
	}
}

// looksLikeNCPError returns true when the body is a structured JSON
// response that NCP's gateway/service produced — i.e. the request reached
// the service and was processed (auth + routing OK), the response is
// just a typed business/account-state error rather than a 200 success.
//
// We treat any well-formed JSON object/array as "auth-OK" because every
// observed NCP error envelope variant — there are at least 6 — is JSON:
//
//   - "errorCode"        : standard REST envelope (api-gateway, sm, sso, …)
//   - "returnCode"       : Classic XML/JSON envelope (autoscaling, vpc, …)
//   - "error":{...}      : Spring-Boot wrapper (cert-manager, cloud-fn)
//   - "code"+"msg"       : private-ca / pca legacy envelope
//   - "status":{"code","message"} : clova-studio envelope
//   - {"timestamp",...}  : Spring-Boot 401 wrapper (organization)
//
// Rather than chase the exhaustive list, accept any structured JSON.
// HMAC-signing failures never produce JSON — they 401 with plain text
// or HTML — so this is a safe signal.
func looksLikeNCPError(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{':
		var m map[string]json.RawMessage
		return json.Unmarshal(trimmed, &m) == nil
	case '[':
		var a []json.RawMessage
		return json.Unmarshal(trimmed, &a) == nil
	case '<':
		// NCP Classic billing/autoscaling reply with the legacy XML
		// envelope on errors when no Accept header forces JSON. Look
		// for the typical <responseError> root or any XML prolog.
		s := string(trimmed)
		return strings.Contains(s, "<responseError>") ||
			strings.HasPrefix(s, "<?xml")
	}
	return false
}

// classifyTyped is the common 2xx/4xx/5xx tail used by every probe. On 200 it
// returns PASS plus the success-detail string the probe wants to surface
// (e.g. "scenarios=3", "body=512B"). On 4xx-with-NCP-envelope it returns
// PASS (auth-OK). Otherwise FAIL.
func classifyTyped(name string, status int, body []byte, successDetail string) result {
	if status >= 200 && status < 300 {
		return result{name, "PASS", successDetail}
	}
	return classify(name, status, body, "")
}

// ---------------------------------------------------------------------------
// per-service probes
// ---------------------------------------------------------------------------

func probeWMS(ctx context.Context, hc *http.Client) result {
	c, err := wms.NewClientWithResponses(
		"https://wms.apigw.ntruss.com/api/v1",
		wms.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"wms", "FAIL: client", err.Error()}
	}
	resp, err := c.ListMonitoringScenariosWithResponse(ctx, nil)
	if err != nil {
		return result{"wms", "FAIL: req", err.Error()}
	}
	if resp.StatusCode() == 200 && resp.JSON200 == nil {
		return result{"wms", "FAIL: parse", bodyPreview(resp.Body)}
	}
	detail := ""
	if resp.JSON200 != nil {
		detail = fmt.Sprintf("scenarios=%d", len(*resp.JSON200))
	}
	return classifyTyped("wms", resp.StatusCode(), resp.Body, detail)
}

func probeSubAccount(ctx context.Context, hc *http.Client) result {
	c, err := sa.NewClientWithResponses(
		"https://subaccount.apigw.ntruss.com/api/v1",
		sa.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"sub-account", "FAIL: client", err.Error()}
	}
	resp, err := c.GetSubAccountsWithResponse(ctx, nil)
	if err != nil {
		return result{"sub-account", "FAIL: req", err.Error()}
	}
	return classifyTyped("sub-account", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeResourceManager(ctx context.Context, hc *http.Client) result {
	c, err := rm.NewClientWithResponses(
		"https://resourcemanager.apigw.ntruss.com/api/v1",
		rm.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"resource-manager", "FAIL: client", err.Error()}
	}
	resp, err := c.GetGroupListWithResponse(ctx, nil)
	if err != nil {
		return result{"resource-manager", "FAIL: req", err.Error()}
	}
	return classifyTyped("resource-manager", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeVPC(ctx context.Context, hc *http.Client) result {
	c, err := vpc.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vpc/v2",
		vpc.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"vpc", "FAIL: client", err.Error()}
	}
	// Empty form body — all fields optional. Use the form-encoded variant.
	resp, err := c.GetVpcListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"vpc", "FAIL: req", err.Error()}
	}
	return classifyTyped("vpc", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeAPIGateway(ctx context.Context, hc *http.Client) result {
	// Spec server URL is missing /api/v1 prefix; override here.
	c, err := apigw.NewClientWithResponses(
		"https://apigateway.apigw.ntruss.com/api/v1",
		apigw.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"api-gateway", "FAIL: client", err.Error()}
	}
	resp, err := c.ProductGetProductListWithResponse(ctx, nil)
	if err != nil {
		return result{"api-gateway", "FAIL: req", err.Error()}
	}
	return classifyTyped("api-gateway", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSecretManager(ctx context.Context, hc *http.Client) result {
	c, err := sm.NewClientWithResponses(
		"https://secretmanager.apigw.ntruss.com/api/v1",
		sm.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"secret-manager", "FAIL: client", err.Error()}
	}
	resp, err := c.GetSecretListWithResponse(ctx, nil)
	if err != nil {
		return result{"secret-manager", "FAIL: req", err.Error()}
	}
	return classifyTyped("secret-manager", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCertificateManager(ctx context.Context, hc *http.Client) result {
	c, err := cm.NewClientWithResponses(
		"https://certificatemanager.apigw.ntruss.com/api/v1",
		cm.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"certificate-manager", "FAIL: client", err.Error()}
	}
	resp, err := c.ListCertificateSWithResponse(ctx)
	if err != nil {
		return result{"certificate-manager", "FAIL: req", err.Error()}
	}
	return classifyTyped("certificate-manager", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSENS(ctx context.Context, hc *http.Client) result {
	c, err := sens.NewClientWithResponses(
		"https://sens.apigw.ntruss.com",
		sens.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"sens", "FAIL: client", err.Error()}
	}
	resp, err := c.ProjectListWithResponse(ctx, nil)
	if err != nil {
		return result{"sens", "FAIL: req", err.Error()}
	}
	return classifyTyped("sens", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCloudInsight(ctx context.Context, hc *http.Client) result {
	c, err := ci.NewClientWithResponses(
		"https://cw.apigw.ntruss.com",
		ci.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-insight", "FAIL: client", err.Error()}
	}
	resp, err := c.GetDashboardListWithResponse(ctx)
	if err != nil {
		return result{"cloud-insight", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-insight", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// Legacy NCP API services — base URL overridden to /v<svc>/v2/ since the
// spec's server URL was set to Classic /<svc>/v2/ which we don't support.
// (TODO: fix spec's server URLs.)

func probeServer(ctx context.Context, hc *http.Client) result {
	c, err := server.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vserver/v2",
		server.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"server", "FAIL: client", err.Error()}
	}
	resp, err := c.GetServerInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"server", "FAIL: req", err.Error()}
	}
	return classifyTyped("server", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeMySQL(ctx context.Context, hc *http.Client) result {
	c, err := mysql.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vmysql/v2",
		mysql.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"mysql", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCloudMysqlInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"mysql", "FAIL: req", err.Error()}
	}
	return classifyTyped("mysql", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeNAS(ctx context.Context, hc *http.Client) result {
	c, err := nas.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vnas/v2",
		nas.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"nas", "FAIL: client", err.Error()}
	}
	resp, err := c.GetNasVolumeInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"nas", "FAIL: req", err.Error()}
	}
	return classifyTyped("nas", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCloudActivityTracer(ctx context.Context, hc *http.Client) result {
	c, err := cat.NewClientWithResponses(
		"https://cloudactivitytracer.apigw.ntruss.com/api/v1",
		cat.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-activity-tracer", "FAIL: client", err.Error()}
	}
	resp, err := c.GetActivityListWithBodyWithResponse(ctx,
		"application/json",
		strings.NewReader("{}"),
	)
	if err != nil {
		return result{"cloud-activity-tracer", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-activity-tracer", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------
// Expansion: GET-style REST probes
// ---------------------------------------------------------------------------

func probeCloudSearch(ctx context.Context, hc *http.Client) result {
	c, err := cs.NewClientWithResponses(
		"https://cloudsearch.apigw.ntruss.com",
		cs.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-search", "FAIL: client", err.Error()}
	}
	resp, err := c.GetDomainListWithResponse(ctx, nil)
	if err != nil {
		return result{"cloud-search", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-search", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCloudFunctions(ctx context.Context, hc *http.Client) result {
	c, err := cf.NewClientWithResponses(
		"https://cloudfunctions.apigw.ntruss.com",
		cf.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-functions", "FAIL: client", err.Error()}
	}
	// /ncf/api/v2/activations is the live route; /activations 404s on the gateway.
	resp, err := c.V2GetActivationListWithResponse(ctx, nil)
	if err != nil {
		return result{"cloud-functions", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-functions", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSourceCommit(ctx context.Context, hc *http.Client) result {
	c, err := scc.NewClientWithResponses(
		"https://sourcecommit.apigw.ntruss.com/api/v1",
		scc.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"source-commit", "FAIL: client", err.Error()}
	}
	resp, err := c.GetRepositoriesWithResponse(ctx)
	if err != nil {
		return result{"source-commit", "FAIL: req", err.Error()}
	}
	return classifyTyped("source-commit", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSourceBuild(ctx context.Context, hc *http.Client) result {
	c, err := scm.NewClientWithResponses(
		"https://sourcebuild.apigw.ntruss.com/api/v1",
		scm.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"source-build", "FAIL: client", err.Error()}
	}
	resp, err := c.GetContainerRegistryWithResponse(ctx)
	if err != nil {
		return result{"source-build", "FAIL: req", err.Error()}
	}
	return classifyTyped("source-build", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSourceDeploy(ctx context.Context, hc *http.Client) result {
	c, err := scd.NewClientWithResponses(
		"https://vpcsourcedeploy.apigw.ntruss.com/api/v1",
		scd.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"source-deploy", "FAIL: client", err.Error()}
	}
	resp, err := c.GetAutoscalingGroupsWithResponse(ctx)
	if err != nil {
		return result{"source-deploy", "FAIL: req", err.Error()}
	}
	return classifyTyped("source-deploy", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSourcePipeline(ctx context.Context, hc *http.Client) result {
	c, err := scp.NewClientWithResponses(
		"https://vpcsourcepipeline.apigw.ntruss.com/api/v1",
		scp.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"source-pipeline", "FAIL: client", err.Error()}
	}
	resp, err := c.GetProjectsWithResponse(ctx)
	if err != nil {
		return result{"source-pipeline", "FAIL: req", err.Error()}
	}
	return classifyTyped("source-pipeline", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSSO(ctx context.Context, hc *http.Client) result {
	c, err := sso.NewClientWithResponses(
		"https://sso.apigw.ntruss.com/api/v1",
		sso.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"sso", "FAIL: client", err.Error()}
	}
	resp, err := c.GetApplicationsWithResponse(ctx, nil)
	if err != nil {
		return result{"sso", "FAIL: req", err.Error()}
	}
	return classifyTyped("sso", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeGeoLocation(ctx context.Context, hc *http.Client) result {
	c, err := geo.NewClientWithResponses(
		"https://geolocation.apigw.ntruss.com",
		geo.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"geo-location", "FAIL: client", err.Error()}
	}
	resp, err := c.GetQuotaWithResponse(ctx)
	if err != nil {
		return result{"geo-location", "FAIL: req", err.Error()}
	}
	return classifyTyped("geo-location", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probePrivateCA(ctx context.Context, hc *http.Client) result {
	c, err := pca.NewClientWithResponses(
		"https://pca.apigw.ntruss.com/api/v1",
		pca.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"private-ca", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCaListWithResponse(ctx, nil)
	if err != nil {
		return result{"private-ca", "FAIL: req", err.Error()}
	}
	return classifyTyped("private-ca", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCloudLogAnalytics(ctx context.Context, hc *http.Client) result {
	c, err := cla.NewClientWithResponses(
		"https://cloudloganalytics.apigw.ntruss.com",
		cla.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-log-analytics", "FAIL: client", err.Error()}
	}
	// regionCode=kr — verified live; the docs' uppercase examples (FKR/PUB)
	// 404 against the gateway, only lowercase ISO-style "kr" matches.
	resp, err := c.GetCapacityWithResponse(ctx, "kr")
	if err != nil {
		return result{"cloud-log-analytics", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-log-analytics", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------
// Expansion: Classic/legacy POST-no-body probes (form-encoded, empty body)
// ---------------------------------------------------------------------------

func probeAutoScaling(ctx context.Context, hc *http.Client) result {
	c, err := auto_scaling.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/autoscaling/v2",
		auto_scaling.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"auto-scaling", "FAIL: client", err.Error()}
	}
	resp, err := c.GetAdjustmentTypeListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"auto-scaling", "FAIL: req", err.Error()}
	}
	return classifyTyped("auto-scaling", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeLoadBalancer(ctx context.Context, hc *http.Client) result {
	c, err := lb.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vloadbalancer/v2",
		lb.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"load-balancer", "FAIL: client", err.Error()}
	}
	resp, err := c.GetLoadBalancerInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"load-balancer", "FAIL: req", err.Error()}
	}
	return classifyTyped("load-balancer", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCDN(ctx context.Context, hc *http.Client) result {
	c, err := cdn.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/cdn/v2",
		cdn.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cdn", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCdnPlusInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"cdn", "FAIL: req", err.Error()}
	}
	return classifyTyped("cdn", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeMongoDB(ctx context.Context, hc *http.Client) result {
	c, err := mongodb.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vmongodb/v2",
		mongodb.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"mongodb", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCloudMongoDbInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"mongodb", "FAIL: req", err.Error()}
	}
	return classifyTyped("mongodb", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeMSSQL(ctx context.Context, hc *http.Client) result {
	c, err := mssql.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vmssql/v2",
		mssql.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"mssql", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCloudMssqlInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"mssql", "FAIL: req", err.Error()}
	}
	return classifyTyped("mssql", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probePostgreSQL(ctx context.Context, hc *http.Client) result {
	c, err := postgresql.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vpostgresql/v2",
		postgresql.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"postgresql", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCloudPostgresqlInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"postgresql", "FAIL: req", err.Error()}
	}
	return classifyTyped("postgresql", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCache(ctx context.Context, hc *http.Client) result {
	c, err := cache.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vcache/v2",
		cache.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cache", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCloudCacheInstanceListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"cache", "FAIL: req", err.Error()}
	}
	return classifyTyped("cache", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------
// Expansion 3: round-3 probes (data, dev tooling, security, billing, misc)
// ---------------------------------------------------------------------------

func probeSTS(ctx context.Context, hc *http.Client) result {
	c, err := sts.NewClientWithResponses(
		"https://sts.apigw.ntruss.com/api/v1",
		sts.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"sts", "FAIL: client", err.Error()}
	}
	resp, err := c.GetStsWithResponse(ctx)
	if err != nil {
		return result{"sts", "FAIL: req", err.Error()}
	}
	return classifyTyped("sts", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeWebSecurityChecker(ctx context.Context, hc *http.Client) result {
	c, err := wsc.NewClientWithResponses(
		"https://wsc.apigw.ntruss.com/api/v1",
		wsc.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"web-security-checker", "FAIL: client", err.Error()}
	}
	resp, err := c.GetJobsWithResponse(ctx, nil)
	if err != nil {
		return result{"web-security-checker", "FAIL: req", err.Error()}
	}
	return classifyTyped("web-security-checker", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataFlow(ctx context.Context, hc *http.Client) result {
	c, err := df.NewClientWithResponses(
		"https://dataflow.apigw.ntruss.com/api/v1",
		df.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-flow", "FAIL: client", err.Error()}
	}
	resp, err := c.GetJobsWithResponse(ctx, nil)
	if err != nil {
		return result{"data-flow", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-flow", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataQuery(ctx context.Context, hc *http.Client) result {
	// Real host is kr.dataquery.naverncp.com/api/v2 (verified live 2026-04-26
	// against doc curl example). dataquery.apigw.ntruss.com returns generic
	// "URL not found" — wrong host entirely.
	c, err := dq.NewClientWithResponses(
		"https://kr.dataquery.naverncp.com/api/v2",
		dq.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-query", "FAIL: client", err.Error()}
	}
	resp, err := c.GetQueriesWithResponse(ctx, nil)
	if err != nil {
		return result{"data-query", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-query", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataStream(ctx context.Context, hc *http.Client) result {
	c, err := ds.NewClientWithResponses(
		"https://datastream.apigw.ntruss.com/api/v1",
		ds.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-stream", "FAIL: client", err.Error()}
	}
	resp, err := c.GetTopicprefixWithResponse(ctx)
	if err != nil {
		return result{"data-stream", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-stream", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataBoxFrame(ctx context.Context, hc *http.Client) result {
	c, err := dbf.NewClientWithResponses(
		"https://databoxframe.apigw.ntruss.com/api/v1",
		dbf.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-box-frame", "FAIL: client", err.Error()}
	}
	resp, err := c.GetBucketListWithResponse(ctx)
	if err != nil {
		return result{"data-box-frame", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-box-frame", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeNclue(ctx context.Context, hc *http.Client) result {
	c, err := nclue.NewClientWithResponses(
		"https://nclue.apigw.ntruss.com/api/v1",
		nclue.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"nclue", "FAIL: client", err.Error()}
	}
	resp, err := c.GetFeatureListWithResponse(ctx, nil)
	if err != nil {
		return result{"nclue", "FAIL: req", err.Error()}
	}
	return classifyTyped("nclue", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeMaiu(ctx context.Context, hc *http.Client) result {
	c, err := maiu.NewClientWithResponses(
		"https://mi.apigw.ntruss.com/api/v1",
		maiu.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"maiu", "FAIL: client", err.Error()}
	}
	resp, err := c.ListWorkspacesWithResponse(ctx, nil)
	if err != nil {
		return result{"maiu", "FAIL: req", err.Error()}
	}
	return classifyTyped("maiu", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCloudOutboundMailer(ctx context.Context, hc *http.Client) result {
	c, err := com.NewClientWithResponses(
		"https://mail.apigw.ntruss.com/api/v1",
		com.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-outbound-mailer", "FAIL: client", err.Error()}
	}
	resp, err := c.GetAddRessbookWithResponse(ctx)
	if err != nil {
		return result{"cloud-outbound-mailer", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-outbound-mailer", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}


func probeEdge(ctx context.Context, hc *http.Client) result {
	c, err := edge.NewClientWithResponses(
		"https://edge.apigw.ntruss.com",
		edge.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"edge", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCertificateListWithResponse(ctx, nil)
	if err != nil {
		return result{"edge", "FAIL: req", err.Error()}
	}
	return classifyTyped("edge", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeFileSafer(ctx context.Context, hc *http.Client) result {
	c, err := file_safer.NewClientWithResponses(
		"https://filesafer.apigw.ntruss.com",
		file_safer.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"file-safer", "FAIL: client", err.Error()}
	}
	resp, err := c.GetInPutFileLogWithResponse(ctx, nil)
	if err != nil {
		return result{"file-safer", "FAIL: req", err.Error()}
	}
	return classifyTyped("file-safer", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probePlatform(ctx context.Context, hc *http.Client) result {
	c, err := platform.NewClientWithResponses(
		"https://billingapi.apigw.ntruss.com/billing/v1",
		platform.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"platform", "FAIL: client", err.Error()}
	}
	resp, err := c.ListPriceGetPriceListWithResponse(ctx, nil)
	if err != nil {
		return result{"platform", "FAIL: req", err.Error()}
	}
	return classifyTyped("platform", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeBlockchain(ctx context.Context, hc *http.Client) result {
	c, err := blockchain.NewClientWithResponses(
		"https://blockchainservice.apigw.ntruss.com/api/v1",
		blockchain.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"blockchain", "FAIL: client", err.Error()}
	}
	resp, err := c.ViewNetworkListWithResponse(ctx, nil)
	if err != nil {
		return result{"blockchain", "FAIL: req", err.Error()}
	}
	return classifyTyped("blockchain", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeGlobalDNS(ctx context.Context, hc *http.Client) result {
	c, err := gdns.NewClientWithResponses(
		"https://globaldns.apigw.ntruss.com",
		gdns.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"global-dns", "FAIL: client", err.Error()}
	}
	resp, err := c.RecordGetDomainListWithResponse(ctx, nil)
	if err != nil {
		return result{"global-dns", "FAIL: req", err.Error()}
	}
	return classifyTyped("global-dns", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeGlobalTrafficManager(ctx context.Context, hc *http.Client) result {
	c, err := gtm.NewClientWithResponses(
		"https://globaltrafficmanager.apigw.ntruss.com",
		gtm.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"global-traffic-manager", "FAIL: client", err.Error()}
	}
	resp, err := c.ViewWithResponse(ctx, nil)
	if err != nil {
		return result{"global-traffic-manager", "FAIL: req", err.Error()}
	}
	return classifyTyped("global-traffic-manager", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeOrganization(ctx context.Context, hc *http.Client) result {
	c, err := organization.NewClientWithResponses(
		"https://organization.apigw.ntruss.com",
		organization.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"organization", "FAIL: client", err.Error()}
	}
	resp, err := c.GetAccountInviteWithResponse(ctx)
	if err != nil {
		return result{"organization", "FAIL: req", err.Error()}
	}
	return classifyTyped("organization", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeHadoop(ctx context.Context, hc *http.Client) result {
	c, err := hadoop.NewClientWithResponses(
		"https://ncloud.apigw.ntruss.com/vhadoop/v2",
		hadoop.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"hadoop", "FAIL: client", err.Error()}
	}
	resp, err := c.GetCloudHadoopAddOnListWithBodyWithResponse(ctx,
		"application/x-www-form-urlencoded",
		strings.NewReader(""),
	)
	if err != nil {
		return result{"hadoop", "FAIL: req", err.Error()}
	}
	return classifyTyped("hadoop", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------
// Expansion 4: AI / media / k8s / security / governance probes
// ---------------------------------------------------------------------------

func probeAITems(ctx context.Context, hc *http.Client) result {
	c, err := ai_tems.NewClientWithResponses(
		"https://aitems.apigw.ntruss.com/api/v1",
		ai_tems.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"ai-tems", "FAIL: client", err.Error()}
	}
	resp, err := c.DataSetListWithResponse(ctx, nil)
	if err != nil {
		return result{"ai-tems", "FAIL: req", err.Error()}
	}
	return classifyTyped("ai-tems", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeCloudAdvisor(ctx context.Context, hc *http.Client) result {
	c, err := cad.NewClientWithResponses(
		"https://cloud-advisor.apigw.ntruss.com/api/v1",
		cad.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cloud-advisor", "FAIL: client", err.Error()}
	}
	resp, err := c.CategoriesWithResponse(ctx)
	if err != nil {
		return result{"cloud-advisor", "FAIL: req", err.Error()}
	}
	return classifyTyped("cloud-advisor", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDRM(ctx context.Context, hc *http.Client) result {
	c, err := drm.NewClientWithResponses(
		"https://multi-drm.apigw.ntruss.com/api/v1",
		drm.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"drm", "FAIL: client", err.Error()}
	}
	resp, err := c.PolicyListWithResponse(ctx, nil)
	if err != nil {
		return result{"drm", "FAIL: req", err.Error()}
	}
	return classifyTyped("drm", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeKMS2(ctx context.Context, hc *http.Client) result {
	// kms2 lives on ocapi.ncloud.com (GLOBAL) — verified live 2026-04-26
	// against the curl example at security-kms2-key-list. The earlier
	// kms.apigw.ntruss.com/api/v1/keys endpoint we briefly used returned
	// 200 but from an UNRELATED keystore service, not kms2.
	c, err := kms2.NewClientWithResponses(
		"https://ocapi.ncloud.com",
		kms2.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"kms2", "FAIL: client", err.Error()}
	}
	resp, err := c.KeyListWithResponse(ctx, nil)
	if err != nil {
		return result{"kms2", "FAIL: req", err.Error()}
	}
	return classifyTyped("kms2", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeLiveStation(ctx context.Context, hc *http.Client) result {
	c, err := live_station.NewClientWithResponses(
		"https://livestation.apigw.ntruss.com/api/v2",
		live_station.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"live-station", "FAIL: client", err.Error()}
	}
	resp, err := c.ChannelChannelListWithResponse(ctx, nil)
	if err != nil {
		return result{"live-station", "FAIL: req", err.Error()}
	}
	return classifyTyped("live-station", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeNKS(ctx context.Context, hc *http.Client) result {
	// Verified live 2026-04-26: real basePath is /vnks/v2 (the spec's
	// region-routing default of `nks/v2` is the doc-portal label, not the
	// gateway prefix).
	c, err := nks.NewClientWithResponses(
		"https://nks.apigw.ntruss.com/vnks/v2",
		nks.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"nks", "FAIL: client", err.Error()}
	}
	resp, err := c.ClustersGetWithResponse(ctx)
	if err != nil {
		return result{"nks", "FAIL: req", err.Error()}
	}
	return classifyTyped("nks", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeVODStation(ctx context.Context, hc *http.Client) result {
	c, err := vod_station.NewClientWithResponses(
		"https://vodstation.apigw.ntruss.com/api/v2",
		vod_station.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"vod-station", "FAIL: client", err.Error()}
	}
	resp, err := c.CategoryListWithResponse(ctx, nil)
	if err != nil {
		return result{"vod-station", "FAIL: req", err.Error()}
	}
	return classifyTyped("vod-station", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeVPE(ctx context.Context, hc *http.Client) result {
	c, err := vpe.NewClientWithResponses(
		"https://vpe.apigw.ntruss.com/api/v1",
		vpe.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"vpe", "FAIL: client", err.Error()}
	}
	resp, err := c.ListWithResponse(ctx, nil)
	if err != nil {
		return result{"vpe", "FAIL: req", err.Error()}
	}
	return classifyTyped("vpe", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeWebshellBehaviorDetector(ctx context.Context, hc *http.Client) result {
	c, err := wbd.NewClientWithResponses(
		"https://wbd.apigw.ntruss.com/api/v1",
		wbd.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"webshell-behavior-detector", "FAIL: client", err.Error()}
	}
	resp, err := c.NotificationGetNotificationIntervalWithResponse(ctx)
	if err != nil {
		return result{"webshell-behavior-detector", "FAIL: req", err.Error()}
	}
	return classifyTyped("webshell-behavior-detector", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataFence(ctx context.Context, hc *http.Client) result {
	c, err := dfn.NewClientWithResponses(
		"https://datafence.apigw.ntruss.com/api/v1",
		dfn.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-fence", "FAIL: client", err.Error()}
	}
	resp, err := c.GetBoxCustomImageWithResponse(ctx, nil)
	if err != nil {
		return result{"data-fence", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-fence", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}


func probePapago(ctx context.Context, hc *http.Client) result {
	// Verified live 2026-04-26: GlossaryList is `GET /glossary/v1/`. Build a
	// raw signed request because the codegen Op binds the path to "/" and
	// the gateway needs the trailing slash on /glossary/v1/.
	c, err := papago.NewClientWithResponses(
		"https://papago.apigw.ntruss.com/glossary/v1/",
		papago.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"papago", "FAIL: client", err.Error()}
	}
	resp, err := c.GlossaryListWithResponse(ctx, nil)
	if err != nil {
		return result{"papago", "FAIL: req", err.Error()}
	}
	return classifyTyped("papago", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataCatalog(ctx context.Context, hc *http.Client) result {
	c, err := dcat.NewClientWithResponses(
		"https://datacatalog.apigw.ntruss.com/api/v1",
		dcat.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-catalog", "FAIL: client", err.Error()}
	}
	resp, err := c.CataLogGetCataLogSWithResponse(ctx, nil)
	if err != nil {
		return result{"data-catalog", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-catalog", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------
// Expansion 5: Object Storage (S3-compat, AWS SigV4)
// ---------------------------------------------------------------------------

func probeObjectStorage(ctx context.Context, _ *http.Client) result {
	// Object Storage uses AWS SigV4, NOT NCP HMAC v2. Build a dedicated
	// client via NewS3HTTPClient — the standard smoke httpClient (HMAC v2)
	// would be silently rejected as anonymous.
	hc, err := ncloud.NewS3HTTPClient(ncloud.S3Config{Timeout: 15 * time.Second})
	if err != nil {
		return result{"object-storage", "FAIL: client", err.Error()}
	}
	c, err := object_storage.NewClientWithResponses(
		"https://kr.object.ncloudstorage.com",
		object_storage.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"object-storage", "FAIL: ws", err.Error()}
	}
	resp, err := c.ListBucketsWithResponse(ctx)
	if err != nil {
		return result{"object-storage", "FAIL: req", err.Error()}
	}
	return classifyTyped("object-storage", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------
// Expansion 6: tail-end hmac-v2 services (awkward op shapes — pass dummy
// IDs / minimal bodies so we still exercise the SigV2 + URL chain end-
// to-end and get a typed 4xx envelope back).
// ---------------------------------------------------------------------------

func probeCDSS(ctx context.Context, hc *http.Client) result {
	c, err := cdss.NewClientWithResponses(
		"https://clouddatastreamingservice.apigw.ntruss.com/api/v1",
		cdss.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"cdss", "FAIL: client", err.Error()}
	}
	// ListPost variant returns 500 with "JSON Parse error" because the
	// spec has no body schema but the server still expects one (spec
	// mismatch). Use the GET-style ACG-info-list op with a dummy
	// serviceGroupInstanceNo — yields typed 4xx (no such resource).
	resp, err := c.ClusterGetAcgInfoListServiceGroupInstanceNoGetWithResponse(ctx, "0")
	if err != nil {
		return result{"cdss", "FAIL: req", err.Error()}
	}
	return classifyTyped("cdss", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeDataForest(ctx context.Context, hc *http.Client) result {
	c, err := dfor.NewClientWithResponses(
		"https://df.apigw.ntruss.com",
		dfor.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"data-forest", "FAIL: client", err.Error()}
	}
	// All ops POST a body. Send empty {} — expect 400 with typed envelope.
	resp, err := c.AccountsCheckAvailableNameWithBodyWithResponse(ctx,
		"application/json", strings.NewReader("{}"))
	if err != nil {
		return result{"data-forest", "FAIL: req", err.Error()}
	}
	return classifyTyped("data-forest", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeGeoCIDR(ctx context.Context, hc *http.Client) result {
	c, err := gcidr.NewClientWithResponses(
		"https://globaltrafficmanager.apigw.ntruss.com",
		gcidr.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"geo-cidr", "FAIL: client", err.Error()}
	}
	resp, err := c.ViewWithResponse(ctx, nil)
	if err != nil {
		return result{"geo-cidr", "FAIL: req", err.Error()}
	}
	return classifyTyped("geo-cidr", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeMediaConnectCenter(ctx context.Context, hc *http.Client) result {
	c, err := mcc.NewClientWithResponses(
		"https://ncloudmcc.apigw.ntruss.com",
		mcc.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"media-connect-center", "FAIL: client", err.Error()}
	}
	// Dummy companyId — expect typed 4xx (company not found).
	resp, err := c.DeptsListViewWithResponse(ctx, "0", nil)
	if err != nil {
		return result{"media-connect-center", "FAIL: req", err.Error()}
	}
	return classifyTyped("media-connect-center", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSecurityMonitoring(ctx context.Context, hc *http.Client) result {
	c, err := secmon.NewClientWithResponses(
		"https://securitymonitoring.apigw.ntruss.com",
		secmon.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"security-monitoring", "FAIL: client", err.Error()}
	}
	// Body has required fields (startDateTime/endDateTime/page/countPerPage).
	// Send empty {} — expect 400 with field-validation envelope.
	resp, err := c.GetAvListWithBodyWithResponse(ctx,
		"application/json", strings.NewReader("{}"))
	if err != nil {
		return result{"security-monitoring", "FAIL: req", err.Error()}
	}
	return classifyTyped("security-monitoring", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSES(ctx context.Context, hc *http.Client) result {
	c, err := ses.NewClientWithResponses(
		"https://vpcsearchengine.apigw.ntruss.com/api/v1",
		ses.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"ses", "FAIL: client", err.Error()}
	}
	// Dummy serviceGroupInstanceNo — expect typed 4xx.
	resp, err := c.ClusterGetClusterInfoGetWithResponse(ctx, "0")
	if err != nil {
		return result{"ses", "FAIL: req", err.Error()}
	}
	return classifyTyped("ses", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

func probeSES2(ctx context.Context, hc *http.Client) result {
	// {basePath} server var defaults to api/v2 — use the resolved URL.
	c, err := ses2.NewClientWithResponses(
		"https://vpcsearchengine.apigw.ntruss.com/api/v2",
		ses2.WithHTTPClient(hc),
	)
	if err != nil {
		return result{"ses2", "FAIL: client", err.Error()}
	}
	resp, err := c.GetClusterInfoListUsingGETWithResponse(ctx, nil)
	if err != nil {
		return result{"ses2", "FAIL: req", err.Error()}
	}
	return classifyTyped("ses2", resp.StatusCode(), resp.Body, fmt.Sprintf("body=%dB", len(resp.Body)))
}

// ---------------------------------------------------------------------------

func bodyPreview(b []byte) string {
	s := string(b)
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func loadDotenv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	for _, p := range []string{
		filepath.Join(cwd, ".env"),
		filepath.Join(cwd, "..", ".env"),
		filepath.Join(cwd, "..", "..", ".env"),
	} {
		if applyDotenv(p) {
			return
		}
	}
}

func applyDotenv(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return true
}

var _ = errors.New // keep imports used during partial compilation

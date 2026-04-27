#!/usr/bin/env python3
"""Emit per-service `endpoints.gen.go` alongside oapi-codegen output.

For every spec in spec/services/{hmac-v2,s3-compat}/, this script
generates:

    services/<pkg>/endpoints.gen.go

containing a ServerEndpoints map keyed by ncloud.Environment and a
`NewFromConfig(cfg ncloud.Config) (*ClientWithResponses, error)`
helper that picks the right base URL from cfg.Env, substitutes any
region-bearing server variables from cfg.Region, and constructs the
underlying http.Client via the appropriate ncloudgo entry point
(NewHTTPClient for HMAC v2, NewS3HTTPClient for SigV4).

This file is invoked from gen/regen.sh after oapi-codegen finishes.
"""
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parent.parent
SPEC_ROOT = Path("/sessions/great-sleepy-hawking/mnt/NcloudGo/ncloud-community-sdk/spec") / "services"
OUT_BASE = REPO_ROOT / "services"

TIER_HMACv2 = "hmac-v2"
TIER_S3 = "s3-compat"
TIERS = (TIER_HMACv2, TIER_S3)

# Variables we resolve at runtime from cfg.Region.
RUNTIME_REGION_VARS = {"regionCode"}
# Variable consumed by x-ncloud-region-routing rules.
BASEPATH_VAR = "basePath"


def slug_to_pkg(slug: str) -> str:
    return slug.replace("-", "_")


def load_spec(path: Path) -> dict:
    with path.open() as f:
        return yaml.safe_load(f)


def extract_servers(spec: dict) -> dict:
    """Return {env: (url_template, variables_dict)} keyed by x-ncloud-env."""
    out = {}
    for srv in spec.get("servers", []) or []:
        env = srv.get("x-ncloud-env")
        if not env:
            continue
        out[env] = (srv["url"], srv.get("variables", {}) or {})
    return out


def extract_availability(spec: dict) -> dict:
    """Return {env: bool}; default True so we always emit something."""
    info = spec.get("info", {}) or {}
    avail = info.get("x-ncloud-availability", {}) or {}
    return {
        "public": bool(avail.get("public", True)),
        "financial": bool(avail.get("financial", True)),
        "gov": bool(avail.get("gov", True)),
    }


def extract_region_rules(spec: dict) -> list | None:
    """Return parsed x-ncloud-region-routing rules or None."""
    rr = spec.get("x-ncloud-region-routing")
    if not rr:
        return None
    return rr.get("rules", []) or None


def materialize_url(template: str, variables: dict) -> str:
    """Resolve known variables. Leave runtime placeholders as
    `{regionCode}` and `{basePath}` so Go code can substitute them at
    request time. Other variables fall back to their declared default.
    """
    def repl(match):
        name = match.group(1)
        if name in RUNTIME_REGION_VARS:
            return "{" + name + "}"
        if name == BASEPATH_VAR:
            return "{" + name + "}"
        var = variables.get(name, {})
        if isinstance(var, dict) and "default" in var:
            return str(var["default"])
        return match.group(0)

    return re.sub(r"\{([A-Za-z_][A-Za-z0-9_]*)\}", repl, template)


def has_placeholder(template: str, name: str) -> bool:
    return ("{" + name + "}") in template


def render_basepath_switch(rules: list) -> str:
    """Render x-ncloud-region-routing rules into a Go function body
    that resolves a basePath string from a `region` variable.
    """
    lines = []
    default_path = None
    for rule in rules:
        match = rule["match"]
        bp = rule["basePath"]
        if match == "default":
            default_path = bp
            continue
        cond = match
        cond = re.sub(r"hasPrefix\(region,\s*'([^']+)'\)", r'strings.HasPrefix(region, "\1")', cond)
        cond = re.sub(r"contains\(region,\s*'([^']+)'\)", r'strings.Contains(region, "\1")', cond)
        cond = cond.replace(" or ", " || ").replace(" and ", " && ")
        cond = re.sub(r"region == '([^']+)'", r'region == "\1"', cond)
        bp_expr = render_basepath_value(bp)
        lines.append(f"\tif {cond} {{\n\t\treturn {bp_expr}\n\t}}")
    if default_path is None:
        default_path = '""'
    else:
        default_path = render_basepath_value(default_path)
    lines.append(f"\treturn {default_path}")
    return "\n".join(lines)


def render_basepath_value(bp: str) -> str:
    """Convert a basePath string template into a Go expression."""
    if "{lowercase(region)}" in bp:
        parts = bp.split("{lowercase(region)}")
        pieces = []
        for i, p in enumerate(parts):
            if p:
                pieces.append(go_str(p))
            if i < len(parts) - 1:
                pieces.append("strings.ToLower(region)")
        return " + ".join(pieces)
    return go_str(bp)


def go_str(s: str) -> str:
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def emit_endpoints_go(slug: str, tier: str, spec: dict) -> str:
    pkg = slug_to_pkg(slug)
    servers = extract_servers(spec)
    avail = extract_availability(spec)
    region_rules = extract_region_rules(spec)

    rendered = {}
    for env, (template, variables) in servers.items():
        if not avail.get(env, True):
            continue
        rendered[env] = materialize_url(template, variables)

    has_region_var = any(has_placeholder(v, "regionCode") for v in rendered.values())
    has_basepath_var = any(has_placeholder(v, BASEPATH_VAR) for v in rendered.values())
    use_sigv4 = tier == TIER_S3
    needs_strings = has_region_var or has_basepath_var or bool(region_rules)

    lines = []
    lines.append("// Code generated by gen/build_endpoints.py. DO NOT EDIT.")
    lines.append("")
    lines.append(f"package {pkg}")
    lines.append("")

    imports = []
    if needs_strings:
        imports.append('"strings"')
    if imports:
        imports.append("")
    imports.append('"github.com/greedylabs/ncloud-community-sdk/sdk"')

    lines.append("import (")
    for imp in imports:
        if imp == "":
            lines.append("")
        else:
            lines.append(f"\t{imp}")
    lines.append(")")
    lines.append("")

    # ServerEndpoints map. Templates keep their {regionCode} / {basePath}
    # placeholders verbatim so callers can introspect which envs need a
    # region.
    lines.append("// ServerEndpoints maps each NCP environment supported by")
    lines.append(f"// the {slug} service to its base URL template. Templates may")
    lines.append("// contain {regionCode} (substituted from cfg.Region at")
    lines.append("// NewFromConfig time) and {basePath} (resolved by")
    lines.append("// resolveBasePath when present).")
    lines.append("var ServerEndpoints = map[ncloud.Environment]string{")
    env_to_const = {
        "public": "ncloud.EnvPublic",
        "financial": "ncloud.EnvFinancial",
        "gov": "ncloud.EnvGov",
    }
    for env in ("public", "financial", "gov"):
        if env in rendered:
            lines.append(f"\t{env_to_const[env]}: {go_str(rendered[env])},")
    lines.append("}")
    lines.append("")

    if region_rules:
        body = render_basepath_switch(region_rules)
        lines.append("// resolveBasePath applies the spec-declared region routing")
        lines.append("// rules to pick the basePath fragment for the current region.")
        lines.append("func resolveBasePath(region string) string {")
        for ln in body.split("\n"):
            lines.append(ln)
        lines.append("}")
        lines.append("")

    # NewFromConfig.
    lines.append("// NewFromConfig builds a ClientWithResponses for the environment")
    lines.append("// and credentials carried by cfg. The HTTP client is the signed")
    lines.append("// transport from the root ncloudgo package — HMAC v2 for the")
    lines.append("// general API surface, AWS SigV4 for Object Storage.")
    lines.append("//")
    lines.append("//\tcfg, _ := ncloud.LoadDefaultConfig(ctx)")
    lines.append(f"//\tc, _   := {pkg}.NewFromConfig(cfg)")
    lines.append("func NewFromConfig(cfg ncloud.Config, opts ...ClientOption) (*ClientWithResponses, error) {")
    lines.append("\tserver, ok := ServerEndpoints[cfg.Env]")
    lines.append("\tif !ok {")
    lines.append("\t\treturn nil, &unsupportedEnvError{env: string(cfg.Env)}")
    lines.append("\t}")
    if has_region_var or has_basepath_var:
        lines.append("\tregion := cfg.Region")
        lines.append("\tif region == \"\" {")
        # SigV4 / object-storage uses lowercase 'kr'; HMAC region routing
        # keys off the uppercase region code (KR/FKR/etc.) per spec.
        default_region = '"kr"' if use_sigv4 else '"KR"'
        lines.append(f"\t\tregion = {default_region}")
        lines.append("\t}")
    if has_region_var:
        lines.append("\tserver = strings.ReplaceAll(server, \"{regionCode}\", region)")
    if has_basepath_var:
        lines.append("\tserver = strings.Replace(server, \"{basePath}\", resolveBasePath(region), 1)")
    if use_sigv4:
        lines.append("\thc, err := ncloud.NewS3HTTPClient(cfg.AsS3Config())")
    else:
        lines.append("\thc, err := ncloud.NewHTTPClient(cfg)")
    lines.append("\tif err != nil {")
    lines.append("\t\treturn nil, err")
    lines.append("\t}")
    lines.append("\tallOpts := append([]ClientOption{WithHTTPClient(hc)}, opts...)")
    lines.append("\treturn NewClientWithResponses(server, allOpts...)")
    lines.append("}")
    lines.append("")

    lines.append("type unsupportedEnvError struct{ env string }")
    lines.append("")
    lines.append("func (e *unsupportedEnvError) Error() string {")
    lines.append(f'\treturn "{pkg}: environment \\"" + e.env + "\\" not supported by {slug}"')
    lines.append("}")

    return "\n".join(lines) + "\n"


def write_endpoints(slug: str, tier: str, spec_path: Path) -> tuple[bool, str]:
    spec = load_spec(spec_path)
    pkg = slug_to_pkg(slug)
    out_dir = OUT_BASE / pkg
    if not out_dir.exists():
        return False, f"skip {slug} (no client output at {out_dir})"
    content = emit_endpoints_go(slug, tier, spec)
    out_path = out_dir / "endpoints.gen.go"
    out_path.write_text(content)
    return True, f"wrote {out_path.relative_to(REPO_ROOT)}"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", help="comma-separated slugs to limit", default="")
    args = ap.parse_args()
    only = set(s.strip() for s in args.only.split(",") if s.strip())

    ok, fail = 0, 0
    for tier in TIERS:
        tier_dir = SPEC_ROOT / tier
        if not tier_dir.is_dir():
            continue
        for spec_path in sorted(tier_dir.glob("*.yaml")):
            slug = spec_path.stem
            if only and slug not in only:
                continue
            try:
                wrote, msg = write_endpoints(slug, tier, spec_path)
                if wrote:
                    ok += 1
                print(f"  {msg}")
            except Exception as e:
                fail += 1
                print(f"  FAIL {slug}: {e}")
    print(f"\nendpoints: {ok} written, {fail} failed")
    return 1 if fail else 0


if __name__ == "__main__":
    sys.exit(main())

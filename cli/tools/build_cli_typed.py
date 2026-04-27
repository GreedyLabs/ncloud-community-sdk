#!/usr/bin/env python3
"""Generate typed cobra wrappers for every SDK service.

Walks ../sdk/services/<svc>/client.gen.go via regex to extract method
signatures (oapi-codegen output is regular enough), cross-references
spec/services/*/<svc>.yaml for human-readable summaries + parameter
descriptions, and emits cli/services/<svc>.go where each cobra
subcommand calls the SDK's typed `<Op>WithResponse` directly.

Output shape per generated file:

    package services

    import (
        "github.com/spf13/cobra"
        clicmd "github.com/greedylabs/ncloud-community-sdk/cli/cmd"
        ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
        wms "github.com/greedylabs/ncloud-community-sdk/sdk/services/wms"
    )

    func init() {
        parent := &cobra.Command{Use: "wms", Short: "..."}
        parent.AddCommand(buildWmsListMonitoringScenarios())
        // ... per op
        clicmd.RegisterServiceCmd(parent)
    }

    func buildWmsListMonitoringScenarios() *cobra.Command {
        // typed param flags + RunE that invokes the SDK directly
    }

Generator scope:
- All ops with no body (GET/DELETE/HEAD): full typed flag wiring
- Ops with body: typed path/query flags + --body / --body-file for the
  request body (uses SDK's `<Op>WithBodyWithResponse` variant which
  takes contentType + io.Reader rather than a typed body struct).
- Path params become positional args on the SDK method call. Their
  Go type comes verbatim from the parsed signature (string, int64,
  BucketName, etc) — generator does the string→typed conversion in
  the RunE.
- Query/header params are fields on the SDK's `<Op>Params` struct.
  Their Go type also comes from parsing the SDK source. Generator
  emits the correct typed conversion per field.

Run:
    python3 cli/tools/build_cli_typed.py
"""
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

import yaml


REPO = Path(__file__).resolve().parent.parent.parent
SDK_DIR = REPO / "sdk" / "services"
SPEC_DIR = REPO / "spec" / "services"
OUT_DIR = REPO / "cli" / "services"


# Regex helpers — oapi-codegen's emit is regular enough for this.
METHOD_RX = re.compile(
    r"""
    ^func\s+\(c\s+\*ClientWithResponses\)\s+
    (?P<name>[A-Z][A-Za-z0-9]*)WithResponse\s*\(
    \s*ctx\s+context\.Context\s*,\s*
    (?P<rest>.*?)
    \)\s*\(\*[A-Za-z0-9_]+HTTPResp\s*,\s*error\s*\)\s*\{
    """,
    re.VERBOSE | re.MULTILINE,
)

WITH_BODY_METHOD_RX = re.compile(
    r"""
    ^func\s+\(c\s+\*ClientWithResponses\)\s+
    (?P<name>[A-Z][A-Za-z0-9]*)WithBodyWithResponse\s*\(
    \s*ctx\s+context\.Context\s*,\s*
    (?P<rest>.*?)
    \)\s*\(\*[A-Za-z0-9_]+HTTPResp\s*,\s*error\s*\)\s*\{
    """,
    re.VERBOSE | re.MULTILINE,
)


def parse_method_args(rest: str) -> tuple[list[tuple[str, str]], str | None]:
    """Split the args between (ctx, ...) and the trailing reqEditors.

    Returns (path_args, params_type).
    path_args is a list of (name, gotype) for positional path-param args.
    params_type is the name of the *<Op>Params struct (without the *) or None.
    """
    # strip trailing `reqEditors ...RequestEditorFn`
    rest = re.sub(r"reqEditors\s*\.\.\.RequestEditorFn\s*,?", "", rest)
    rest = rest.strip().rstrip(",").strip()
    if not rest:
        return [], None

    # split by comma but careful about generic type expressions —
    # in our SDK there are none, so naive split works.
    parts = [p.strip() for p in rest.split(",") if p.strip()]
    path_args = []
    params_type = None
    for p in parts:
        # `name type` with maybe pointer
        m = re.match(r"^(\w+)\s+\*?([\w.]+)$", p)
        if not m:
            continue
        name, gotype = m.group(1), m.group(2)
        if name == "params":
            params_type = gotype
        elif name in ("contentType", "body"):
            # body method extras — caller handles separately
            continue
        else:
            path_args.append((name, gotype))
    return path_args, params_type


# Field declaration inside a Params struct, e.g.:
#   Name *string `form:"name,omitempty" json:"name,omitempty"`
PARAM_FIELD_RX = re.compile(
    r"^\s*(?P<field>[A-Z][A-Za-z0-9]*)\s+(?P<type>\*?[\w.]+)\s+`[^`]*\bform:\"(?P<wire>[^\",]+)",
    re.MULTILINE,
)


def parse_params_struct(client_src: str, params_type: str) -> list[dict]:
    """Find `type <params_type> struct { ... }` and extract its fields.

    Returns a list of dicts: {wire_name, go_field, go_type}.
    Skips embedded structs / non-form fields.
    """
    if not params_type:
        return []
    block_rx = re.compile(
        rf"type\s+{re.escape(params_type)}\s+struct\s*\{{(?P<body>.*?)\}}\s*\n",
        re.DOTALL,
    )
    m = block_rx.search(client_src)
    if not m:
        return []
    body = m.group("body")
    out = []
    for fm in PARAM_FIELD_RX.finditer(body):
        out.append({
            "wire": fm.group("wire"),
            "go_field": fm.group("field"),
            "go_type": fm.group("type"),
        })
    return out


TYPE_ALIAS_RX = re.compile(r"^type\s+(\w+)\s+(int|int32|int64|string|bool|float32|float64)\s*$", re.MULTILINE)
PRIMITIVE_TYPES = {"string", "int", "int32", "int64", "bool", "float32", "float64"}


def is_complex_type(base: str, aliases: dict[str, str]) -> bool:
    """True when the type is a struct (needs JSON unmarshal) rather than
    a primitive or alias-of-primitive (settable via direct cast)."""
    if base in PRIMITIVE_TYPES:
        return False
    if base in aliases:
        return False
    if "." in base:
        # qualified pkg.Type — no way to know without parsing the
        # other package; default to "complex" so we use json
        # unmarshal which is the safe path.
        return True
    # bare name not in our local SDK alias map → assume struct
    return True


def parse_type_aliases(client_src: str) -> dict[str, str]:
    """Map type-alias name → underlying primitive (for enum conversion)."""
    return {m.group(1): m.group(2) for m in TYPE_ALIAS_RX.finditer(client_src)}


def go_type_to_flag_init(gotype: str, svc_pkg: str, aliases: dict[str, str] | None = None) -> tuple[str, str]:
    """Return (var_decl_type, conversion_template).

    var_decl_type is the Go type for the local flag-bound variable
    (always a non-pointer, since cobra's StringVar / Int64Var etc
    write through a non-pointer).

    conversion_template turns the local variable into the typed value
    needed by the SDK call. Use {var} as a placeholder for the local
    variable name.
    """
    base = gotype.lstrip("*")
    # well-known scalars
    if base == "string":
        return "string", "{var}"
    if base == "int":
        return "string", 'mustAtoi({var})'
    if base == "int32":
        return "string", 'int32(mustAtoi({var}))'
    if base == "int64":
        return "string", 'mustAtoi64({var})'
    if base == "bool":
        return "string", 'mustParseBool({var})'
    if base == "float32":
        return "string", 'float32(mustParseFloat({var}))'
    if base == "float64":
        return "string", 'mustParseFloat({var})'
    # already qualified pkg.Type — special-case the openapi_types
    # UUID since it's a [16]byte alias that can't be string-cast.
    if base == "openapi_types.UUID":
        return "string", "mustParseUUID({var})"
    if "." in base:
        return "string", f"{base}({{var}})"
    # bare type from the same SDK package — qualify with svc_pkg.
    # If it's a known type alias on top of an int / bool / float, parse
    # the string first then cast.
    underlying = (aliases or {}).get(base)
    if underlying == "int":
        return "string", f"{svc_pkg}.{base}(mustAtoi({{var}}))"
    if underlying == "int32":
        return "string", f"{svc_pkg}.{base}(int32(mustAtoi({{var}})))"
    if underlying == "int64":
        return "string", f"{svc_pkg}.{base}(mustAtoi64({{var}}))"
    if underlying == "bool":
        return "string", f"{svc_pkg}.{base}(mustParseBool({{var}}))"
    if underlying == "float32":
        return "string", f"{svc_pkg}.{base}(float32(mustParseFloat({{var}})))"
    if underlying == "float64":
        return "string", f"{svc_pkg}.{base}(mustParseFloat({{var}}))"
    # default — string-based alias or unknown
    return "string", f"{svc_pkg}.{base}({{var}})"


def go_str(s: str) -> str:
    if s is None:
        return '""'
    out = s.replace("\\", "\\\\").replace('"', '\\"')
    out = out.replace("\n", " ").replace("\r", " ").replace("\t", " ")
    out = re.sub(r" {2,}", " ", out).strip()
    return '"' + out + '"'


def kebab(name: str) -> str:
    s = re.sub(r"(.)([A-Z][a-z]+)", r"\1-\2", name)
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1-\2", s)
    return s.lower()


def slug_to_pkg(slug: str) -> str:
    return slug.replace("-", "_")


def find_spec_for_slug(slug: str) -> dict | None:
    for tier in ("hmac-v2", "s3-compat"):
        p = SPEC_DIR / tier / f"{slug}.yaml"
        if p.exists():
            return yaml.safe_load(p.read_text())
    return None


def collect_spec_op_meta(spec: dict) -> dict[str, dict]:
    """Index the spec's operations by operationId for cross-reference."""
    out = {}
    for path, methods in (spec.get("paths") or {}).items():
        if not isinstance(methods, dict):
            continue
        for method, op in methods.items():
            if not isinstance(op, dict):
                continue
            opid = op.get("operationId")
            if not opid:
                continue
            out[opid] = {
                "summary": op.get("summary", "") or "",
                "description": op.get("description", "") or "",
                "method": method.upper(),
                "path": path,
                "params": op.get("parameters") or [],
            }
    return out


def emit_op_func(svc_pkg: str, sdk_pkg_path: str, slug: str, op: dict, sigs: dict, aliases: dict[str, str]) -> str:
    """Render the buildXxxYyy() Go function for one operation."""
    op_name = op["op_name"]                    # "ListMonitoringScenarios"
    cmd_word = kebab(op_name)
    summary = op["summary"]
    description = op["description"]
    has_body = op["has_body"]
    path_args = op["path_args"]                # [(name, type), ...]
    params_type = op["params_type"]            # "ListMonitoringScenariosParams" or None
    params_fields = op["params_fields"]        # [{wire, go_field, go_type}, ...]
    spec_params = op["spec_params"]            # spec parameters list, by wire-name

    fn_name = "build" + svc_pkg.title().replace("_", "") + op_name

    # ---- 1. local var declarations + flag registrations ----
    var_decls = []
    flag_lines = []
    # Path args (required)
    for arg_name, arg_type in path_args:
        var_decls.append(f"\tvar arg_{arg_name} string")
        # description from spec if available (try matching by name)
        desc = ""
        for sp in spec_params:
            if sp.get("in") == "path" and sp.get("name") == arg_name:
                desc = sp.get("description", "") or ""
                break
        if not desc:
            desc = f"(path) {arg_name}"
        flag_lines.append(
            f"\tc.Flags().StringVar(&arg_{arg_name}, {go_str(arg_name)}, \"\", {go_str('[PATH] ' + desc)})"
        )
        flag_lines.append(f"\t_ = c.MarkFlagRequired({go_str(arg_name)})")

    # Params struct fields (query/header)
    for f in params_fields:
        var_decls.append(f"\tvar p_{f['go_field']} string")
        # cobra flag — kebab name from wire
        wire = f["wire"]
        # description from spec
        desc = ""
        for sp in spec_params:
            if sp.get("name") == wire:
                desc = sp.get("description", "") or ""
                break
        if not desc:
            desc = f"(query) {wire}"
        flag_lines.append(
            f"\tc.Flags().StringVar(&p_{f['go_field']}, {go_str(wire)}, \"\", {go_str(desc)})"
        )

    # Body flags
    if has_body:
        var_decls.append("\tvar bodyFile, bodyInline string")
        flag_lines.append('\tc.Flags().StringVar(&bodyFile, "body-file", "", "Path to a JSON file containing the request body.")')
        flag_lines.append('\tc.Flags().StringVar(&bodyInline, "body", "", "Inline JSON request body (alternative to --body-file).")')

    # ---- 2. RunE body ----
    runE = []
    runE.append("\t\t\tcfg, err := clicmd.ConfigFromCtx(c)")
    runE.append("\t\t\tif err != nil { return err }")
    runE.append(f"\t\t\tclient, err := {svc_pkg}.NewFromConfig(cfg)")
    runE.append("\t\t\tif err != nil { return err }")

    # path arg conversions
    call_args = ["c.Context()"]
    for arg_name, arg_type in path_args:
        _, conv_tpl = go_type_to_flag_init(arg_type, svc_pkg, aliases)
        conv = conv_tpl.replace("{var}", f"arg_{arg_name}")
        if conv == f"arg_{arg_name}":
            # plain string, pass through
            call_args.append(f"arg_{arg_name}")
        else:
            runE.append(f"\t\t\tpa_{arg_name} := {conv}")
            call_args.append(f"pa_{arg_name}")

    # params struct construction
    if params_type:
        runE.append(f"\t\t\tparams := &{svc_pkg}.{params_type}{{}}")
        for f in params_fields:
            wire = f["wire"]
            field = f["go_field"]
            gotype = f["go_type"]
            is_ptr = gotype.startswith("*")
            base = gotype.lstrip("*")
            assign_target = f"params.{field}"
            runE.append(f'\t\t\tif c.Flags().Changed({go_str(wire)}) {{')
            if is_complex_type(base, aliases):
                # Struct-typed field — accept JSON literal in the flag,
                # unmarshal into the typed struct.
                qualified = base if "." in base else f"{svc_pkg}.{base}"
                runE.append(f"\t\t\t\tvar v {qualified}")
                runE.append(f"\t\t\t\tif err := json.Unmarshal([]byte(p_{field}), &v); err != nil {{")
                runE.append(f'\t\t\t\t\treturn fmt.Errorf("--%s: %w", {go_str(wire)}, err)')
                runE.append("\t\t\t\t}")
                if is_ptr:
                    runE.append(f"\t\t\t\t{assign_target} = &v")
                else:
                    runE.append(f"\t\t\t\t{assign_target} = v")
            else:
                _, conv_tpl = go_type_to_flag_init(gotype, svc_pkg, aliases)
                conv = conv_tpl.replace("{var}", f"p_{field}")
                if base == "string" or conv == f"p_{field}":
                    rhs_value = f"p_{field}"
                else:
                    runE.append(f"\t\t\t\tv := {conv}")
                    rhs_value = "v"
                if is_ptr:
                    runE.append(f"\t\t\t\tlocal := {rhs_value}")
                    runE.append(f"\t\t\t\t{assign_target} = &local")
                else:
                    runE.append(f"\t\t\t\t{assign_target} = {rhs_value}")
            runE.append("\t\t\t}")
        call_args.append("params")
    elif has_body:
        # Some body ops still want a nil params slot
        # (only if the SDK signature has params) — handled by sig extraction
        pass

    # SDK method call
    if has_body:
        # need contentType + body io.Reader
        runE.append("\t\t\tvar body io.Reader")
        runE.append('\t\t\tif bodyFile != "" {')
        runE.append("\t\t\t\tf, ferr := os.Open(bodyFile)")
        runE.append('\t\t\t\tif ferr != nil { return fmt.Errorf("open --body-file: %w", ferr) }')
        runE.append("\t\t\t\tdefer f.Close()")
        runE.append("\t\t\t\tbody = f")
        runE.append('\t\t\t} else if bodyInline != "" {')
        runE.append("\t\t\t\tbody = bytes.NewBufferString(bodyInline)")
        runE.append("\t\t\t}")
        call_args.append('"application/json"')
        call_args.append("body")
        method_name = f"{op_name}WithBodyWithResponse"
    else:
        method_name = f"{op_name}WithResponse"

    runE.append(f"\t\t\tresp, err := client.{method_name}({', '.join(call_args)})")
    runE.append("\t\t\tif err != nil { return err }")
    runE.append("\t\t\tif apiErr := ncloud.AsAPIError(resp.HTTPResponse, resp.Body); apiErr != nil {")
    runE.append("\t\t\t\treturn apiErr")
    runE.append("\t\t\t}")
    runE.append("\t\t\treturn clicmd.Render(c.OutOrStdout(), pickPayload(resp))")

    # ---- 3. assemble ----
    body = (
        f"func {fn_name}() *cobra.Command {{\n"
        + "\n".join(var_decls) + ("\n" if var_decls else "")
        + "\tc := &cobra.Command{\n"
        f"\t\tUse: {go_str(cmd_word)},\n"
        f"\t\tShort: {go_str(summary)},\n"
        f"\t\tLong: {go_str(description)},\n"
        "\t\tRunE: func(c *cobra.Command, _ []string) error {\n"
        + "\n".join(runE) + "\n"
        "\t\t},\n"
        "\t}\n"
        + "\n".join(flag_lines) + ("\n" if flag_lines else "")
        + "\treturn c\n"
        "}\n"
    )
    return fn_name, body


def emit_service_file(slug: str, spec: dict) -> str | None:
    pkg = slug_to_pkg(slug)
    client_path = SDK_DIR / pkg / "client.gen.go"
    if not client_path.exists():
        return None
    src = client_path.read_text()

    # Collect ops from SDK source (the no-body methods).
    plain_methods = []
    for m in METHOD_RX.finditer(src):
        op_name = m.group("name")
        # Skip body-flavoured variants — handled by the dedicated
        # WITH_BODY_METHOD_RX pass below. We prefer the raw `WithBody`
        # form for the CLI (accepts arbitrary JSON / form bodies via
        # --body / --body-file) and ignore the typed FormdataBody
        # alternative — every service that has Formdata also has the
        # raw Body variant on the same op.
        if op_name.endswith("WithBody") or op_name.endswith("WithFormdataBody") or op_name.endswith("WithJSONBody"):
            continue
        path_args, params_type = parse_method_args(m.group("rest"))
        plain_methods.append({
            "op_name": op_name,
            "path_args": path_args,
            "params_type": params_type,
            "params_fields": parse_params_struct(src, params_type) if params_type else [],
            "has_body": False,
        })

    # Body methods (POST/PUT/PATCH).
    body_methods = []
    for m in WITH_BODY_METHOD_RX.finditer(src):
        op_name = m.group("name")
        path_args, params_type = parse_method_args(m.group("rest"))
        body_methods.append({
            "op_name": op_name,
            "path_args": path_args,
            "params_type": params_type,
            "params_fields": parse_params_struct(src, params_type) if params_type else [],
            "has_body": True,
        })

    # When the SDK has BOTH a typed body method (WithResponse) and a
    # raw-body method (WithBodyWithResponse) for the same op, we
    # prefer the raw-body variant for the CLI (lets users pass JSON
    # files directly without us reconstructing the typed struct).
    body_op_names = {m["op_name"] for m in body_methods}
    plain_methods = [m for m in plain_methods if m["op_name"] not in body_op_names]
    methods = plain_methods + body_methods
    if not methods:
        return None

    spec_meta = collect_spec_op_meta(spec)
    aliases = parse_type_aliases(src)

    # Cross-reference + emit each operation.
    fn_decls = []
    fn_calls = []
    used_path_typed_conv = set()
    for m in sorted(methods, key=lambda x: x["op_name"]):
        meta = spec_meta.get(m["op_name"], {})
        m["summary"] = meta.get("summary", "")
        m["description"] = meta.get("description", "")
        m["spec_params"] = meta.get("params", [])
        fn_name, fn_body = emit_op_func(pkg, "", slug, m, src, aliases)
        fn_decls.append(fn_body)
        fn_calls.append(f"\tparent.AddCommand({fn_name}())")
        for _, t in m["path_args"]:
            base = t.lstrip("*")
            if base in ("int", "int32", "int64", "bool", "float32", "float64"):
                used_path_typed_conv.add(base)
        for f in m["params_fields"]:
            base = f["go_type"].lstrip("*")
            if base in ("int", "int32", "int64", "bool", "float32", "float64"):
                used_path_typed_conv.add(base)

    # Service-level metadata for the parent command.
    svc_short = (spec.get("info") or {}).get("title") or slug

    imports = [
        '"bytes"',
        '"encoding/json"',
        '"fmt"',
        '"io"',
        '"os"',
        '',
        '"github.com/spf13/cobra"',
        '',
        'clicmd "github.com/greedylabs/ncloud-community-sdk/cli/cmd"',
        'ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"',
        f'{pkg} "github.com/greedylabs/ncloud-community-sdk/sdk/services/{pkg}"',
    ]

    body = []
    body.append("// Code generated by tools/build_cli_typed.py. DO NOT EDIT.")
    body.append("")
    body.append("package services")
    body.append("")
    body.append("import (")
    for imp in imports:
        if imp == "":
            body.append("")
        else:
            body.append("\t" + imp)
    body.append(")")
    body.append("")
    # silence unused imports for services that don't need every one
    # mark imports as used (some ops only use a subset)
    body.append("var _ = bytes.NewBuffer")
    body.append("var _ = json.Unmarshal")
    body.append("var _ = fmt.Sprintf")
    body.append("var _ io.Reader = nil")
    body.append("var _ = os.Open")
    body.append("var _ = ncloud.AsAPIError")
    body.append("")

    # init() registers the service tree
    body.append("func init() {")
    body.append(f"\tparent := &cobra.Command{{ Use: {go_str(slug)}, Short: {go_str(svc_short)} }}")
    body.extend(fn_calls)
    body.append("\tclicmd.RegisterServiceCmd(parent)")
    body.append("}")
    body.append("")
    body.extend(fn_decls)
    return "\n".join(body)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", help="comma-separated slugs", default="")
    args = ap.parse_args()
    only = set(s.strip() for s in args.only.split(",") if s.strip())

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for stale in OUT_DIR.glob("*.gen.go"):
        stale.unlink()

    ok = skip = 0
    for tier in ("hmac-v2", "s3-compat"):
        for spec_path in sorted((SPEC_DIR / tier).glob("*.yaml")):
            slug = spec_path.stem
            if only and slug not in only:
                continue
            spec = yaml.safe_load(spec_path.read_text())
            content = emit_service_file(slug, spec)
            if not content:
                skip += 1
                print(f"  skip {slug} (no client.gen.go or no methods)")
                continue
            (OUT_DIR / f"{slug_to_pkg(slug)}.gen.go").write_text(content)
            ok += 1
            print(f"  wrote {slug}")
    print(f"\nGenerated {ok} services, {skip} skipped")
    return 0


if __name__ == "__main__":
    sys.exit(main())

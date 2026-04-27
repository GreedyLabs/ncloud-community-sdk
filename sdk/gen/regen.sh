#!/usr/bin/env bash
# regen.sh — invoke oapi-codegen for every HMAC v2 spec.
#
# Spec layout:
#   spec/services/hmac-v2/      ← what we generate (single auth model)
#   spec/services/hmac-v1/      ← legacy KMS only — NOT generated
#   spec/services/ncloud-app/   ← per-app credentials — NOT generated
#   spec/services/s3-compat/    ← AWS SigV4 (Object Storage) — NOT generated
#
# Only the hmac-v2 tier shares a single credential model that the SDK can
# meaningfully unify. The other three tiers are kept in the spec catalog
# for documentation / hand-rolled clients but are deliberately out of
# scope for this SDK's generated clients.
#
# Requires:
#   - github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen on $PATH
#   - The spec/ git submodule populated (run `git submodule update --init` first).
#
# Per-service config overrides live under gen/configs/<slug>.yaml — when
# present, oapi-codegen uses that file. Otherwise a default is generated
# in-memory.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# We generate clients for two auth tiers:
#   hmac-v2/   — the bulk of NCP services (signed by ncloudgo.NewHTTPClient)
#   s3-compat/ — Object Storage, signed by ncloudgo.NewS3HTTPClient (SigV4)
# Both land under services/<slug>/ so external imports stay flat.
SPEC_DIRS=(
  "$REPO_ROOT/spec/services/hmac-v2"
  "$REPO_ROOT/spec/services/s3-compat"
)
SPEC_DIR="${SPEC_DIRS[0]}"  # primary; iteration below covers all
# Public per-service packages (NOT under internal/, so external consumers
# can import e.g. github.com/greedylabs/ncloud-community-sdk-go/services/wms).
OUT_BASE="$REPO_ROOT/services"
CONFIGS_DIR="$REPO_ROOT/gen/configs"

for d in "${SPEC_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "ERROR: $d missing — run 'git submodule update --init' first" >&2
    exit 1
  fi
done

if ! command -v oapi-codegen >/dev/null 2>&1; then
  echo "ERROR: oapi-codegen not on PATH. Install:" >&2
  echo "  go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest" >&2
  exit 1
fi

mkdir -p "$OUT_BASE"

ok=0
fail=0
fail_list=()
# Collect all .yaml files across the configured spec dirs.
spec_files=()
for d in "${SPEC_DIRS[@]}"; do
  for f in "$d"/*.yaml; do
    [ -e "$f" ] && spec_files+=("$f")
  done
done

for spec in "${spec_files[@]}"; do
  slug="$(basename "$spec" .yaml)"
  pkg="$(echo "$slug" | tr '-' '_')"           # Go package name (underscores)
  out_dir="$OUT_BASE/$pkg"                     # Output dir mirrors package name
  mkdir -p "$out_dir"

  cfg_file="$CONFIGS_DIR/$slug.yaml"
  if [ -f "$cfg_file" ]; then
    cfg_arg="-config $cfg_file"
  else
    tmp_cfg="$(mktemp -t oapi-XXXXXX.yaml)"
    # embedded-spec: false — keep generated files lean. Each consumer can
    # import the spec separately if needed.
    cat > "$tmp_cfg" <<EOF
package: $pkg
output: $out_dir/client.gen.go
generate:
  models: true
  client: true
  embedded-spec: false
output-options:
  skip-prune: false
  # Suffix the auto-generated per-operation response wrapper type so it
  # doesn't collide with our hand-written schemas (e.g. spec defines
  # \`CreateAutoScalingGroupResponse\` schema, oapi would otherwise also
  # emit a wrapper of the same name).
  response-type-suffix: HTTPResp
EOF
    cfg_arg="-config $tmp_cfg"
  fi

  if oapi-codegen $cfg_arg "$spec" 2>/tmp/oapi-err.log; then
    ok=$((ok + 1))
    [ -n "${tmp_cfg:-}" ] && rm -f "$tmp_cfg" && unset tmp_cfg
  else
    fail=$((fail + 1))
    fail_list+=("$slug")
    err=$(head -2 /tmp/oapi-err.log | tr '\n' ' ' | head -c 200)
    echo "  FAIL $slug: $err"
    [ -n "${tmp_cfg:-}" ] && rm -f "$tmp_cfg" && unset tmp_cfg
  fi
done

echo
echo "Generated $ok service clients, $fail failed"
if [ "$fail" -gt 0 ]; then
  printf "  Failed: %s\n" "${fail_list[*]}"
fi

# After oapi-codegen produces client.gen.go for each service, layer the
# AWS-style endpoints.gen.go on top — provides ServerEndpoints map and
# NewFromConfig(cfg) helper. This step depends on PyYAML; install if
# missing.
echo
echo "Generating endpoints.gen.go (AWS-style NewFromConfig helpers)…"
if ! python3 -c 'import yaml' 2>/dev/null; then
  echo "  installing PyYAML…"
  python3 -m pip install --quiet --user PyYAML
fi
python3 "$REPO_ROOT/gen/build_endpoints.py"

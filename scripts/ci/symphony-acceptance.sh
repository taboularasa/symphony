#!/usr/bin/env bash
# Merge-authoritative symphony acceptance command (HAD-2389).
# It accepts no arguments or overrides. Every prerequisite and proof is
# mandatory.
set -euo pipefail
fail() { echo "FAIL: $*" >&2; exit 1; }
[ "$#" -eq 0 ] || fail "arguments and policy overrides are forbidden"
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

expected_attestation_schema=1
policy=".tangled/ci-policy.yaml"
workflow=".tangled/workflows/symphony-ci.yml"
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
started_epoch="$(date +%s)"

for command in git go python3 sha256sum; do
  command -v "$command" >/dev/null 2>&1 || fail "required baked dependency absent: $command"
done
[ -f "$policy" ] || fail "repository CI policy absent"
[ -f "$workflow" ] || fail "acceptance workflow absent"

# Job-start admission. A repository may not infer image capability from source
# presence, so this reads the builder-owned attestation and refuses before any
# repository command executes -- a substrate mismatch must not be reportable as
# a test failure.
attestation_file="/etc/hadto-spindle-image.json"
[ -r "$attestation_file" ] || fail "guest publishes no build attestation: $attestation_file"
observed_attestation="$(
  python3 - "$attestation_file" "$expected_attestation_schema" <<'PY'
import hashlib, json, pathlib, sys

document = json.loads(pathlib.Path(sys.argv[1]).read_text())
if document.get("algorithm") != "sha256":
    raise SystemExit("guest build attestation uses an unsupported digest algorithm")
if int(document.get("schemaVersion", 0)) < int(sys.argv[2]):
    raise SystemExit("guest build attestation schema is below the required minimum")
# Recompute rather than read: an attestation that does not hash to its own
# recorded digest is a copied or edited one, and must not admit a job.
canonical = json.dumps(document["build"], sort_keys=True, separators=(",", ":")).encode()
if hashlib.sha256(canonical).hexdigest() != document["attestationDigest"]:
    raise SystemExit("guest build attestation does not hash to its own recorded digest")
print(document["attestationDigest"])
PY
)" || fail "guest build attestation was rejected"

# This module's dependencies are vendored in-tree, so the lane resolves nothing
# at run time. Enforce that rather than hope for it: a guest with no egress that
# silently fell back to the module proxy would fail late and confusingly.
[ -d vendor ] || fail "vendored dependency tree absent"
[ -f vendor/modules.txt ] || fail "vendor/modules.txt absent"
export GOFLAGS="-mod=vendor"
export GOPROXY=off
export GONOSUMDB='*'
export GOSUMDB=off

run_required() {
  name=$1; shift
  start=$(date +%s)
  "$@" || fail "required command failed: $name"
  printf 'EVIDENCE command=%s result=pass duration_seconds=%s\n' "$name" "$(( $(date +%s) - start ))"
}

# `go mod verify` proves the vendored tree matches the module hashes the go.sum
# records, so a hand-edited vendor directory is refused rather than trusted.
run_required mod-verify go mod verify
run_required vet go vet ./...
run_required build go build ./...
run_required tests go test ./...

source_sha="$(git rev-parse --verify HEAD^{commit})"
policy_sha="$(sha256sum "$policy" | cut -d' ' -f1)"
workflow_sha="$(sha256sum "$workflow" | cut -d' ' -f1)"
vendor_sha="$(sha256sum vendor/modules.txt | cut -d' ' -f1)"
finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf '%s\n' \
  "EVIDENCE schema=symphony.ci.v1 result=pass" \
  "EVIDENCE source_sha=$source_sha policy_sha256=sha256:$policy_sha workflow_sha256=sha256:$workflow_sha" \
  "EVIDENCE image_attestation=sha256:$observed_attestation vendor_modules_sha256=sha256:$vendor_sha goproxy=$GOPROXY" \
  "EVIDENCE started_at=$started_at finished_at=$finished_at duration_seconds=$(( $(date +%s) - started_epoch ))"

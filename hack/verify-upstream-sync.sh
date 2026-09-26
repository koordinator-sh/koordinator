#!/bin/sh
# verify-upstream-sync.sh — detect drift between forked upstream code and its source.
#
# Usage: hack/verify-upstream-sync.sh
#
# Checks:
#   1. The pinned k8s.io/kubernetes version in upstream_primitives.go matches go.mod.
#   2. Pure-copy functions (prioritizeNodes, findNodesThatPassExtenders) are identical
#      to upstream after import-alias normalization.
#
# Run this after every k8s.io/kubernetes version bump in go.mod.

set -eu

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PRIMITIVES_FILE="${REPO_ROOT}/pkg/scheduler/equivalence/upstream_primitives.go"
GOMOD_FILE="${REPO_ROOT}/go.mod"

find_go() {
    for candidate in /usr/local/go/bin/go /usr/lib/go/bin/go; do
        if [ -x "${candidate}" ]; then
            echo "${candidate}"
            return
        fi
    done
    resolved=$(command -v go 2>/dev/null) || true
    if [ -n "${resolved}" ] && [ -x "${resolved}" ]; then
        echo "${resolved}"
        return
    fi
    echo "FAIL: go binary not found" >&2
    exit 1
}

# extract_func prints one function from <file> <name>. It matches both upstream's method form
# ("func (sched *Scheduler) name(") and the local free-function form ("func name(").
extract_func() {
    awk -v name="$2" '
        $0 ~ "^func (\\([^)]*\\) )?" name "\\(" { capture=1 }
        capture { print }
        capture && /^}/ { exit }
    ' "$1"
}

TMPDIR_BASE=$(mktemp -d)
trap 'rm -rf "${TMPDIR_BASE}"' EXIT

fail=0

# --- 1. Version pin ---

pinned_version=$(sed -n 's|.*Forked from k8s\.io/kubernetes@\(v[0-9][0-9.]*\)/.*|\1|p' "${PRIMITIVES_FILE}" | head -1)
gomod_version=$(sed -n 's|^[[:space:]]*k8s\.io/kubernetes \(v[0-9][0-9.]*\).*|\1|p' "${GOMOD_FILE}" | head -1)

if [ -z "${pinned_version}" ]; then
    echo "FAIL: no version pin found in upstream_primitives.go"
    exit 1
fi

if [ "${pinned_version}" != "${gomod_version}" ]; then
    echo "FAIL: version mismatch — pinned ${pinned_version}, go.mod ${gomod_version}"
    echo "  Update the file header, re-diff the forked functions, then re-run."
    exit 1
fi

echo "OK: version pin matches go.mod (${pinned_version})"

# --- 2. Pure-copy function diff ---

GOMODCACHE=$("$(find_go)" env GOMODCACHE)
UPSTREAM_FILE="${GOMODCACHE}/k8s.io/kubernetes@${pinned_version}/pkg/scheduler/schedule_one.go"

if [ ! -f "${UPSTREAM_FILE}" ]; then
    echo "FAIL: upstream source not found: ${UPSTREAM_FILE}"
    echo "  Run: go mod download k8s.io/kubernetes"
    exit 1
fi

for fn in prioritizeNodes findNodesThatPassExtenders hasScoring hasExtenderFilters; do
    # Normalize the two expected shape differences back to upstream's, so the comparison is over
    # the bodies alone: import aliases (fwktype->fwk, corev1->v1) and the receiver-turned-first-
    # parameter ("func name(sched *scheduler.Scheduler," -> "func (sched *Scheduler) name(").
    extract_func "${PRIMITIVES_FILE}" "${fn}" |
        sed 's/fwktype\./fwk./g; s/corev1\./v1./g' |
        sed 's/^func '"${fn}"'(sched \*scheduler\.Scheduler, /func (sched *Scheduler) '"${fn}"'(/' |
        sed 's/^func '"${fn}"'(sched \*scheduler\.Scheduler)/func (sched *Scheduler) '"${fn}"'()/' > "${TMPDIR_BASE}/local.tmp"
    extract_func "${UPSTREAM_FILE}" "${fn}" > "${TMPDIR_BASE}/upstream.tmp"

    if [ ! -s "${TMPDIR_BASE}/upstream.tmp" ]; then
        echo "FAIL: upstream no longer has func ${fn} — manual review required"
        fail=1
        continue
    fi

    if diff -u "${TMPDIR_BASE}/upstream.tmp" "${TMPDIR_BASE}/local.tmp" > "${TMPDIR_BASE}/diff.tmp" 2>&1; then
        echo "OK: ${fn} matches upstream"
    else
        echo "FAIL: ${fn} has drift from upstream:"
        sed 's/^/    /' "${TMPDIR_BASE}/diff.tmp"
        fail=1
    fi
done

if [ "${fail}" -ne 0 ]; then
    exit 1
fi
echo "PASS"

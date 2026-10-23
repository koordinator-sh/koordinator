#!/bin/sh
# verify-upstream-sync.sh — detect drift between the scheduler code forked from upstream Kubernetes
# and the upstream source of the k8s.io/kubernetes version go.mod resolves to.
#
# Usage: hack/verify-upstream-sync.sh (or make verify-upstream-sync)
#
# Checks:
#   1. Every k8s.io/kubernetes@<version> reference in the forked files names the version go.mod
#      resolves k8s.io/kubernetes to.
#   2. Pure-copy functions in upstream_primitives.go are identical to upstream after import-alias
#      and receiver normalization.
#   3. Every function equivalence_schedule.go mirrors with local divergences (an "Upstream:" line
#      naming a Scheduler method) points at the line upstream declares it on, and the upstream body
#      still has the SHA-256 recorded in its "Upstream-SHA256:" line. A changed hash means upstream
#      changed the function: re-review the mirror and its DIFF notes, then record the new hash.
#   4. The mirrored package constants keep their upstream values.
#
# CI runs it on every change. Run it locally after every k8s.io/kubernetes bump in go.mod.

set -eu

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EQUIVALENCE_DIR="${REPO_ROOT}/pkg/scheduler/equivalence"
PRIMITIVES_FILE="${EQUIVALENCE_DIR}/upstream_primitives.go"
MIRRORS_FILE="${EQUIVALENCE_DIR}/equivalence_schedule.go"

find_go() {
    resolved=$(command -v go 2>/dev/null) || true
    if [ -n "${resolved}" ] && [ -x "${resolved}" ]; then
        echo "${resolved}"
        return
    fi
    for candidate in /usr/local/go/bin/go /usr/lib/go/bin/go; do
        if [ -x "${candidate}" ]; then
            echo "${candidate}"
            return
        fi
    done
    echo "FAIL: go binary not found" >&2
    exit 1
}

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum | cut -d' ' -f1
    else
        shasum -a 256 | cut -d' ' -f1
    fi
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

GO=$(find_go)

# module_field prints a field of the k8s.io/kubernetes module go.mod resolves to, following a
# replace directive when there is one.
module_field() {
    (cd "${REPO_ROOT}" && "${GO}" list -m -f "{{if .Replace}}{{.Replace.$1}}{{else}}{{.$1}}{{end}}" k8s.io/kubernetes)
}

TMPDIR_BASE=$(mktemp -d)
trap 'rm -rf "${TMPDIR_BASE}"' EXIT

fail=0

gomod_version=$(module_field Version)
if [ -z "${gomod_version}" ]; then
    echo "FAIL: go.mod replaces k8s.io/kubernetes with a local directory, which has no version to verify against"
    exit 1
fi
upstream_dir=$(module_field Dir)
if [ -z "${upstream_dir}" ] || [ ! -d "${upstream_dir}" ]; then
    (cd "${REPO_ROOT}" && "${GO}" mod download k8s.io/kubernetes)
    upstream_dir=$(module_field Dir)
fi
UPSTREAM_FILE="${upstream_dir}/pkg/scheduler/schedule_one.go"
if [ ! -f "${UPSTREAM_FILE}" ]; then
    echo "FAIL: upstream source not found: ${UPSTREAM_FILE}"
    exit 1
fi

# --- 1. Version references ---

refs=$(grep -ho 'k8s\.io/kubernetes@v[0-9][0-9.]*' "${PRIMITIVES_FILE}" "${MIRRORS_FILE}" | sort -u)
if [ -z "${refs}" ]; then
    echo "FAIL: no k8s.io/kubernetes version reference found in the forked files"
    exit 1
fi
for ref in ${refs}; do
    if [ "${ref#k8s.io/kubernetes@}" != "${gomod_version}" ]; then
        echo "FAIL: ${ref} does not match go.mod (k8s.io/kubernetes ${gomod_version})"
        echo "  Update the references, re-review the forked functions, then re-run."
        fail=1
    fi
done
[ "${fail}" -eq 0 ] && echo "OK: version references match go.mod (${gomod_version})"

# --- 2. Pure-copy function diff ---

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

# --- 3. Mirrors with local divergences ---

# One "name line hash reviewed" record per mirrored function. hash and reviewed are "-" when the
# function records no Upstream-SHA256 line.
awk '
    /^\/\/ Upstream: k8s\.io\/kubernetes@[^ ]*\/pkg\/scheduler\/schedule_one\.go:[0-9]+ \(Scheduler\.[A-Za-z]+\)/ {
        line = $3; sub(/.*:/, "", line)
        name = $4; gsub(/[()]/, "", name); sub(/^Scheduler\./, "", name)
        hash = "-"; reviewed = "-"; pending = 1
        next
    }
    pending && /^\/\/ Upstream-SHA256: [0-9a-f]+ \(reviewed at v[0-9.]+\)$/ {
        hash = $3; reviewed = $6; sub(/\)$/, "", reviewed)
        next
    }
    pending && /^func / { print name, line, hash, reviewed; pending = 0 }
' "${MIRRORS_FILE}" > "${TMPDIR_BASE}/mirrors.tmp"

mirror_count=$(wc -l < "${TMPDIR_BASE}/mirrors.tmp" | tr -d ' ')
reference_count=$(grep -c '^// Upstream: k8s\.io/kubernetes@' "${MIRRORS_FILE}" || true)
if [ "${mirror_count}" -eq 0 ] || [ "${mirror_count}" -ne "${reference_count}" ]; then
    echo "FAIL: parsed ${mirror_count} of ${reference_count} Upstream references in equivalence_schedule.go"
    echo "  Each mirror needs \"// Upstream: k8s.io/kubernetes@<version>/pkg/scheduler/schedule_one.go:<line> (Scheduler.<name>)\"."
    fail=1
fi

GOMODCACHE=$("${GO}" env GOMODCACHE)
while read -r name line hash reviewed; do
    upstream_line=$(grep -n "^func (sched \*Scheduler) ${name}(" "${UPSTREAM_FILE}" | cut -d: -f1)
    if [ -z "${upstream_line}" ]; then
        echo "FAIL: upstream no longer has Scheduler.${name} — manual review required"
        fail=1
        continue
    fi
    if [ "${upstream_line}" != "${line}" ]; then
        echo "FAIL: the reference of ${name} points at schedule_one.go:${line}, upstream declares it at :${upstream_line}"
        fail=1
    fi

    upstream_hash=$(extract_func "${UPSTREAM_FILE}" "${name}" | sha256)
    if [ "${hash}" = "${upstream_hash}" ]; then
        echo "OK: ${name} mirrors an unchanged upstream body (reviewed at ${reviewed})"
        continue
    fi
    fail=1
    if [ "${hash}" = "-" ]; then
        echo "FAIL: ${name} records no upstream hash. After reviewing the mirror, add below its Upstream line:"
    else
        echo "FAIL: upstream changed Scheduler.${name} since it was reviewed at ${reviewed}."
        echo "  Re-review the mirror and its DIFF notes against the change, then record:"
    fi
    echo "    // Upstream-SHA256: ${upstream_hash} (reviewed at ${gomod_version})"
    reviewed_file="${GOMODCACHE}/k8s.io/kubernetes@${reviewed}/pkg/scheduler/schedule_one.go"
    if [ "${reviewed}" != "-" ] && [ "${reviewed}" != "${gomod_version}" ]; then
        if [ -f "${reviewed_file}" ]; then
            extract_func "${reviewed_file}" "${name}" > "${TMPDIR_BASE}/reviewed.tmp"
            extract_func "${UPSTREAM_FILE}" "${name}" > "${TMPDIR_BASE}/upstream.tmp"
            echo "  Upstream change since ${reviewed}:"
            diff -u "${TMPDIR_BASE}/reviewed.tmp" "${TMPDIR_BASE}/upstream.tmp" | sed 's/^/    /' || true
        else
            echo "  Run \"go mod download k8s.io/kubernetes@${reviewed}\" to see the upstream change."
        fi
    fi
done < "${TMPDIR_BASE}/mirrors.tmp"

# --- 4. Mirrored constants ---

for name in minFeasibleNodesToFind minFeasibleNodesPercentageToFind; do
    mirrored_value=$(sed -n "s/^[[:space:]]*${name} = \([0-9][0-9]*\)$/\1/p" "${MIRRORS_FILE}")
    upstream_value=$(sed -n "s/^[[:space:]]*${name} = \([0-9][0-9]*\)$/\1/p" "${UPSTREAM_FILE}")
    if [ -z "${upstream_value}" ] || [ "${mirrored_value}" != "${upstream_value}" ]; then
        echo "FAIL: ${name} is ${mirrored_value:-missing} here, ${upstream_value:-missing} upstream"
        fail=1
    else
        echo "OK: ${name} matches upstream (${upstream_value})"
    fi
done

if [ "${fail}" -ne 0 ]; then
    exit 1
fi
echo "PASS"

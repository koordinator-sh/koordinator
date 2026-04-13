#!/bin/bash

# Copyright 2022 The Koordinator Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# check-deprecated-apis.sh
#
# This script scans Go source files for usage of deprecated Kubernetes API versions.
# It reports findings as warnings (not failures) since some backward-compatibility
# usage may be intentional.
#
# Deprecated APIs checked:
# - policy/v1beta1 (PDB - deprecated since K8s 1.21)
# - batch/v1beta1 (CronJob - deprecated since K8s 1.21)
# - autoscaling/v2beta1 and autoscaling/v2beta2 (HPA - deprecated since K8s 1.23)
# - flowcontrol.apiserver.k8s.io/v1beta1, v1beta2, v1beta3 (deprecated/removed)

set -o pipefail

# Colors for output
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# Root directory of the project
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

# Directories to scan
SCAN_DIRS="pkg cmd apis"

# Temporary file to store results
RESULTS_FILE=$(mktemp)
trap "rm -f ${RESULTS_FILE}" EXIT

# Counter for findings
TOTAL_FINDINGS=0
TOTAL_FILES=0

echo "========================================"
echo "Checking for Deprecated Kubernetes APIs"
echo "========================================"
echo ""

# Function to search for a deprecated API
search_deprecated_api() {
    local api_pattern="$1"
    local api_name="$2"
    local deprecation_info="$3"
    
    local findings=""
    local file_count=0
    local finding_count=0
    
    # Search for the pattern in Go files, excluding vendor/, zz_generated*, and test files
    findings=$(grep -rn --include="*.go" "${api_pattern}" ${SCAN_DIRS} 2>/dev/null | \
        grep -v "vendor/" | \
        grep -v "zz_generated" | \
        grep -v "_test.go" || true)
    
    if [ -n "${findings}" ]; then
        echo -e "${YELLOW}[WARNING] Found usage of deprecated API: ${api_name}${NC}"
        echo -e "  ${deprecation_info}"
        echo ""
        
        while IFS= read -r line; do
            if [ -n "${line}" ]; then
                echo "  ${line}"
                ((finding_count++))
            fi
        done <<< "${findings}"
        
        file_count=$(echo "${findings}" | cut -d: -f1 | sort -u | wc -l | tr -d ' ')
        
        echo ""
        echo "  -> Found ${finding_count} occurrence(s) in ${file_count} file(s)"
        echo ""
        
        TOTAL_FINDINGS=$((TOTAL_FINDINGS + finding_count))
        TOTAL_FILES=$((TOTAL_FILES + file_count))
    fi
}

# Check for policy/v1beta1 (PodDisruptionBudget)
search_deprecated_api \
    '"k8s.io/api/policy/v1beta1"' \
    "policy/v1beta1" \
    "PodDisruptionBudget - deprecated since Kubernetes 1.21, use policy/v1"

# Check for batch/v1beta1 (CronJob)
search_deprecated_api \
    '"k8s.io/api/batch/v1beta1"' \
    "batch/v1beta1" \
    "CronJob - deprecated since Kubernetes 1.21, use batch/v1"

# Check for autoscaling/v2beta1 (HPA)
search_deprecated_api \
    '"k8s.io/api/autoscaling/v2beta1"' \
    "autoscaling/v2beta1" \
    "HorizontalPodAutoscaler - deprecated since Kubernetes 1.23, use autoscaling/v2"

# Check for autoscaling/v2beta2 (HPA)
search_deprecated_api \
    '"k8s.io/api/autoscaling/v2beta2"' \
    "autoscaling/v2beta2" \
    "HorizontalPodAutoscaler - deprecated since Kubernetes 1.23, use autoscaling/v2"

# Check for flowcontrol.apiserver.k8s.io/v1beta1
search_deprecated_api \
    'flowcontrol.apiserver.k8s.io/v1beta1' \
    "flowcontrol.apiserver.k8s.io/v1beta1" \
    "FlowControl - deprecated, use flowcontrol.apiserver.k8s.io/v1"

# Check for flowcontrol.apiserver.k8s.io/v1beta2
search_deprecated_api \
    'flowcontrol.apiserver.k8s.io/v1beta2' \
    "flowcontrol.apiserver.k8s.io/v1beta2" \
    "FlowControl - deprecated, use flowcontrol.apiserver.k8s.io/v1"

# Check for flowcontrol.apiserver.k8s.io/v1beta3
search_deprecated_api \
    'flowcontrol.apiserver.k8s.io/v1beta3' \
    "flowcontrol.apiserver.k8s.io/v1beta3" \
    "FlowControl - deprecated, use flowcontrol.apiserver.k8s.io/v1"

# Summary
echo "========================================"
echo "Summary"
echo "========================================"
if [ ${TOTAL_FINDINGS} -gt 0 ]; then
    echo -e "${YELLOW}Found ${TOTAL_FINDINGS} deprecated API usage(s) in ${TOTAL_FILES} file(s)${NC}"
    echo ""
    echo "Note: These are warnings only. Some backward-compatibility usage may be intentional."
    echo "Please review and update to newer API versions where appropriate."
else
    echo -e "${GREEN}No deprecated Kubernetes API usage found.${NC}"
fi

# Always exit 0 (warning mode, not blocking)
exit 0

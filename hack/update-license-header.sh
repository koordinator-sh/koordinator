#!/usr/bin/env bash

PROJECT=$(cd $(dirname $0)/..; pwd)

LICENSEHEADERCHECKER_VERSION=v1.3.0

GOBIN=${PROJECT}/bin go install github.com/lsm-dev/license-header-checker/cmd/license-header-checker@${LICENSEHEADERCHECKER_VERSION}

${PROJECT}/bin/license-header-checker -r -a -v -i vendor ${PROJECT}/hack/boilerplate.go.txt . go

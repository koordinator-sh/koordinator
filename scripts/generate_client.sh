#!/usr/bin/env bash

go mod vendor
retVal=$?
if [ $retVal -ne 0 ]; then
    exit $retVal
fi

set -e
TMP_DIR=$(mktemp -d)
mkdir -p "${TMP_DIR}"/src/github.com/koordinator-sh/koordinator/pkg/client
cp -r ./{apis,hack,vendor} "${TMP_DIR}"/src/github.com/koordinator-sh/koordinator/

(cd "${TMP_DIR}"/src/github.com/koordinator-sh/koordinator; \
    GOPATH=${TMP_DIR} GO111MODULE=off /bin/bash vendor/k8s.io/code-generator/generate-groups.sh all \
    github.com/koordinator-sh/koordinator/pkg/client github.com/koordinator-sh/koordinator/apis "config:v1alpha1 slo:v1alpha1" -h ./hack/boilerplate.go.txt)

ls -l "${TMP_DIR}"/src/github.com/koordinator-sh/koordinator/pkg/client/
rm -rf ./pkg/client/{clientset,informers,listers}
mv "${TMP_DIR}"/src/github.com/koordinator-sh/koordinator/pkg/client/* ./pkg/client

if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' 's#\"config\"#\"config.koordinator.sh\"#g' ./pkg/client/clientset/versioned/typed/config/v1alpha1/fake/fake_*.go
    sed -i '' 's#\"slo\"#\"slo.koordinator.sh\"#g' ./pkg/client/clientset/versioned/typed/slo/v1alpha1/fake/fake_*.go
fi

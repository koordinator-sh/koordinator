#!/usr/bin/env bash

cd $(dirname $0)

cd ../
bin/license-header-checker -r -a -v -i vendor "hack/boilerplate.go.txt" . go

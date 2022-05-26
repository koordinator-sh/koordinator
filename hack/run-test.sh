#!/bin/bash

cd $(dirname $0)/..
PWD=$(pwd)
BASENAME=$(basename $0)
OPTIONS=$@

function usage() {
    echo "Usage: "
    echo "  $BASENAME"
    echo "  $BASENAME test_pattern (Test* | *.go)"
}

function run_test() {
    pattern=$1
    if [[ $pattern == *.go ]]; then
        pattern=`basename $pattern`
        pkgs=`find ./pkg -name "$pattern" | xargs -i dirname {} | tr -s '\n' ' '`
        while read -r line
        do
            pkg=${line#".//"}
            echo go test -timeout 30s "github.com/koordinator-sh/koordinator/$pkg" -v
            go test -timeout 30s "github.com/koordinator-sh/koordinator/$pkg" -v
        done <<< "$pkgs"
    else
        pkgs=`grep "$pattern" -r ./pkg | cut -d: -f1 | grep -E ".go$" | xargs -i dirname {}`
        while read -r line
        do
            pkg=${line#".//"}
            echo go test -timeout 30s "github.com/koordinator-sh/koordinator/$pkg" -run ^$pattern$ -v
            go test -timeout 30s "github.com/koordinator-sh/koordinator/$pkg" -run ^$pattern$ -v
        done <<< "$pkgs"
    fi
}

case $1 in
    -*)
        usage
        exit 0
        ;;
    "")
        go test -timeout 30s github.com/koordinator-sh/koordinator/pkg/... -v
        ;;
    *)
        run_test $1
        ;;
esac

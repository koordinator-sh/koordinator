#!/usr/bin/env bash

# replace corrected plural/singular name of nodeslo crd.
sed -in-place -e 's/  name: nodesloes.slo.koordinator.sh/  name: nodeslos.slo.koordinator.sh/g' config/crd/bases/slo.koordinator.sh_nodesloes.yaml
sed -in-place -e 's/plural: nodesloes$/plural: nodeslos/g' config/crd/bases/slo.koordinator.sh_nodesloes.yaml

rm -f config/crd/bases/slo.koordinator.sh_nodesloes.yamln-place
mv config/crd/bases/slo.koordinator.sh_nodesloes.yaml config/crd/bases/slo.koordinator.sh_nodeslos.yaml


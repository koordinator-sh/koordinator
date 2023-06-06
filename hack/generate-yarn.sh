#!/usr/bin/env bash
#
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

set -o errexit
set -o nounset
set -o pipefail

KOORDINATOR_PKG_ROOT="github.com/koordinator-sh/koordinator"
KOORDINATOR_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
KOORDINATOR_YARN_ROOT="${KOORDINATOR_ROOT}/pkg/yarn"
KOORDINATOR_YARN_API_PATH="${KOORDINATOR_YARN_ROOT}/apis/proto"
YARN_API_FILES="$( find ${KOORDINATOR_YARN_API_PATH} -name "*.proto" )"
echo ">> generate go pkgs for yarn proto files in ${KOORDINATOR_YARN_API_PATH}"
echo ">> api file names: ${YARN_API_FILES}"

# input: "./hack/../pkg/yarn/apis/proto/hadoopyarn/server/resourcemanager_administration_protocol.proto"
function generate_code() {
  yarn_api_proto=${1}
  file_name="$( echo ${yarn_api_proto} | grep -Eo "[a-z,_,A-Z]*.proto$" )" # resourcemanager_administration_protocol.proto
  yarn_api_out_path=${yarn_api_proto%/*.proto} # "./hack/../pkg/yarn/apis/proto/hadoopyarn/server

  file_pkg_map_str="$( generate_import_files_pkg_map ${file_name} )"
  file_pkg_map=($file_pkg_map_str)
  echo ">> api file ${file_name} has related pkg:" "${file_pkg_map[@]}"

  # --go_opt=MSecurity.proto=github.com/koordinator-sh/koordiantor/pkg/yarn/apis/proto/hadoopcommon
  # --go-grpc_opt=MSecurity.proto=github.com/koordinator-sh/koordiantor/pkg/yarn/apis/proto/hadoopcommon
  PKG_NAME_ARGS=""
  RPC_PKG_NAME_ARGS=""
  for file_pkg_kv in "${file_pkg_map[@]}"
  do
    file_name="${file_pkg_kv%%:*}" #
    pkg_name="${file_pkg_kv##*:}"
    PKG_NAME_ARGS+="--go_opt=M${file_name}=${pkg_name} "
    RPC_PKG_NAME_ARGS+="--go-grpc_opt=M${file_name}=${pkg_name} "
  done

  #proto_cmd="protoc ${PROTO_PATH_ARGS} ${PKG_NAME_ARGS} --go_opt=paths=source_relative --go_out=${yarn_api_out_path} --go-grpc_opt=paths=source_relative ${RPC_PKG_NAME_ARGS} --go-grpc_out=${yarn_api_out_path} ${yarn_api_proto}"
  proto_cmd="protoc ${PROTO_PATH_ARGS} ${PKG_NAME_ARGS} --go_opt=paths=source_relative --go_out=${yarn_api_out_path} ${yarn_api_proto}"

  echo ">> ready to generate for ${file_name}, protoc command:"
  echo "${proto_cmd}"
  ${proto_cmd}
}


# input: resourcemanager_administration_protocol.proto or server/yarn_server_resourcemanager_service_protos.proto
# output: (yarn_server_resourcemanager_service_protos.proto:koordinator-sh/koordinator/pkg/yarn/apis/proto/hadoopyarn/server)
function generate_import_files_pkg_map() {
  input_file_name=${1}
  file_name="$( echo "${input_file_name}" | grep -Eo "[a-z,_,A-Z]*.proto$" )" # resourcemanager_administration_protocol.proto
  file_path="$( find ${KOORDINATOR_YARN_API_PATH} -name ${file_name} )"

  file_pkg_map=()
  file_relative_path=${file_path##${KOORDINATOR_ROOT}} # /pkg/yarn/apis/proto/hadoopyarn/server/resourcemanager_administration_protocol.proto
  pkg_relative_path=${file_relative_path%/*.proto} # /pkg/yarn/apis/proto/hadoopyarn/server
  file_pkg=${KOORDINATOR_PKG_ROOT}${pkg_relative_path} # github.com/koordiantor-sh/koordiantor/pkg/yarn/apis/proto/hadoopyarn/server
  file_pkg_map+=("${input_file_name}:${file_pkg}")

  # server/yarn_server_resourcemanager_service_protos.proto
  import_paths=("$( grep -E "import \".*.proto\";" ${file_path} | grep -Eo "\".*\"" | sed "s/\"//g" )")
  if [ ! -z "${import_paths}" ]; then
    for import_path in "${import_paths[@]}"
    do
        import_file_name="$( echo ${import_path} | grep -Eo "[a-z,_,A-Z]*.proto$" )" # yarn_server_resourcemanager_service_protos.proto
        import_file_path="$( find ${KOORDINATOR_YARN_API_PATH} -name ${import_file_name} )" # ./hack/../pkg/yarn/apis/proto/hadoopyarn/server/yarn_server_resourcemanager_service_protos.proto
        file_pkg_map+=("$( generate_import_files_pkg_map ${import_path} )")
    done
  fi
  echo "${file_pkg_map[@]}"
}

yarn_api_pkg_relative_path_list=()
for yarn_api_file in ${YARN_API_FILES}
do
    # e.g. /pkg/yarn/apis/proto/hadoopcommon/IpcConnectionContext.proto
    yarn_api_file_relative_path=${yarn_api_file##${KOORDINATOR_ROOT}}
    # e.g. /pkg/yarn/apis/proto/hadoopcommon
    yarn_api_pkg_relative_path=${yarn_api_file_relative_path%/*.proto}
    yarn_api_pkg_relative_path_list+=("${yarn_api_pkg_relative_path}")
done
# from lower to update to get the shortest go module args
yarn_api_pkg_relative_path_list=($(echo "${yarn_api_pkg_relative_path_list[@]}" | tr ' ' '\n' | sort -ur | tr '\n' ' '))
echo ">> proto api file paths: " "${yarn_api_pkg_relative_path_list[@]}"

# --proto_path=./hack/../pkg/yarn/apis/proto/hadoopcommon --proto_path=./hack/../pkg/yarn/apis/proto/hadoopyarn --proto_path=./hack/../pkg/yarn/apis/proto/hadoopyarn/server
PROTO_PATH_ARGS=""
for yarn_api_pkg_relative_path in "${yarn_api_pkg_relative_path_list[@]}"
do
    PROTO_PATH_ARGS+="--proto_path=${KOORDINATOR_ROOT}${yarn_api_pkg_relative_path} "
done

# generate go pkg for each api file
for api_file_path in $YARN_API_FILES
do
  api_file_name="$( echo "${api_file_path}" | grep -Eo "[a-z,_,A-Z]*.proto$" )"
  echo ">> generate go pkg for ${api_file_name}"
  generate_code ${api_file_path}
done

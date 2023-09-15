#!/usr/bin/env bash
#
# Copyright 2017 The Kubernetes Authors.
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
#

if [ -f /etc/redhat-release ]; then
    # RedHat or CentOS 
    sudo yum -y update && \
        sudo yum -y install cmake wget && \
        sudo yum -y groupinstall "Development Tools"
elif [ -f /etc/debian_version ]; then
    # Debian or Ubuntu 
    sudo apt-get update && \
        sudo apt-get -y install bash build-essential cmake wget
elif [ $(uname) == "Darwin" ]; then
    echo "skip macOS"
else
    echo "failed to install c++ build tools and wget, please install them manually."
    exit 1
fi

export LIBPFM4VERSION="4.13.0"

wget https://sourceforge.net/projects/perfmon2/files/libpfm4/libpfm-$LIBPFM4VERSION.tar.gz && \
        echo "bcb52090f02bc7bcb5ac066494cd55bbd5084e65  libpfm-$LIBPFM4VERSION.tar.gz" | sha1sum -c && \
        tar -xzf libpfm-$LIBPFM4VERSION.tar.gz && \
        rm libpfm-$LIBPFM4VERSION.tar.gz

export DBG="-g -Wall" && \
  make -e -C libpfm-$LIBPFM4VERSION && \
  sudo make install -C libpfm-$LIBPFM4VERSION

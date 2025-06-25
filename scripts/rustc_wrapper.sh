#!/bin/bash
#
# Copyright (C) 2025 The Android Open Source Project
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

# A wrapper around rustc/clippy to set up the correct absolute path for OUT_DIR.
# This has to be done on invocation rather than precalucalted in Soong to
# ensure that RBE calculates its own absolute path rather than inheriting the
# local host absolute path.
#
# The first argument should be the path to rustc/clippy

# Check if $SOONG_RUST_GEN_DIR is set, otherwise don't do anything.
if [ ! -z "${SOONG_RUST_GEN_DIR}" ]; then
  if [[ $SOONG_RUST_GEN_DIR =~ ^/ ]]; then
    export OUT_DIR=$SOONG_RUST_GEN_DIR
  else
    export OUT_DIR=`pwd`/$SOONG_RUST_GEN_DIR
  fi
fi

exec_path=$1
shift
exec $exec_path "$@"

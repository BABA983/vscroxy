#!/bin/sh
# Rebuild the Postman sandbox library bundles in internal/request/js/vendor.
# Requires Node and npm. The npm lock file pins every input version and
# integrity hash; build/hashes.txt records the generated outputs.
set -eu
cd "$(dirname "$0")"
npm ci --ignore-scripts --no-audit --no-fund
exec go run ./build

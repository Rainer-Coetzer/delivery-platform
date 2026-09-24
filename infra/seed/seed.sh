#!/bin/sh
# Seeds demo data. The logic lives in tools/seed (Go, stdlib only) so it
# needs no jq and fails with the full HTTP error when something is wrong.
set -e
cd "$(dirname "$0")/../.."
exec go run ./tools/seed

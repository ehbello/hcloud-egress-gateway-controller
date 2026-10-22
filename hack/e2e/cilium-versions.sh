#!/usr/bin/env bash
# Print the Cilium versions the datapath e2e runs against, as a GitHub Actions matrix:
#
#   - the newest patch release of every minor from MIN_MINOR on (the supported range: the
#     cilium backend needs egressGateways, Cilium >= 1.18), which must pass;
#   - the newest pre-release of a minor beyond the newest stable one, if there is one
#     (1.21.0-pre.N, -rc.N), which runs as "experimental": a failure there is the early
#     warning, weeks before that minor is released, not a red build.
#
# Resolved from the GitHub releases on every run, so the matrix never goes stale.
set -euo pipefail
MIN_MINOR=${MIN_MINOR:-18}

gh api 'repos/cilium/cilium/releases?per_page=100' | jq -c --argjson min "$MIN_MINOR" '
  [ .[] | select(.draft | not) | .tag_name | ltrimstr("v") ] as $tags
  | [ $tags[] | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$")) | split(".") | map(tonumber) ] as $stable
  | [ $stable[] | select(.[0] > 1 or .[1] >= $min) ]
    | group_by(.[0:2]) | map(max_by(.[2]) | map(tostring) | join(".")) as $lines
  | ($stable | max | .[0:2]) as $newest
  | [ $tags[] | select(test("-"))
      | select((split("-")[0] | split(".") | map(tonumber) | .[0:2]) > $newest) ] | first as $pre
  | [ $lines[] | {version: ., experimental: false} ]
    + (if $pre then [{version: $pre, experimental: true}] else [] end)'

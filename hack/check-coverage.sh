#!/usr/bin/env bash
set -euo pipefail

check_pkg_cov() {
  local pkg="$1"
  local threshold="$2"

  local output
  output=$(go test "${pkg}" -cover -count=1)
  echo "${output}"

  local cov
  cov=$(echo "${output}" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p' | tail -n1)
  if [[ -z "${cov}" ]]; then
    echo "failed to parse coverage for ${pkg}"
    exit 1
  fi

  awk -v cov="${cov}" -v threshold="${threshold}" 'BEGIN { exit (cov+0 < threshold+0) ? 1 : 0 }' || {
    echo "coverage gate failed for ${pkg}: ${cov}% < ${threshold}%"
    exit 1
  }

  echo "coverage gate passed for ${pkg}: ${cov}% >= ${threshold}%"
}

check_pkg_cov ./controllers 65
check_pkg_cov ./pkg/recorder 80
check_pkg_cov ./pkg/dispatcher 85


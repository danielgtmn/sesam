#!/usr/bin/env bash
# Tests for scripts/commit-type.sh. Run: scripts/commit-type_test.sh
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
fails=0

# expect <want> <subject> [<body>]: want is major, minor, patch, none or invalid.
expect() {
  local want=$1 got
  shift
  if got=$(scripts/commit-type.sh "$@" 2>/dev/null); then :; else got=invalid; fi
  if [ "$got" = "$want" ]; then
    echo "ok    $want  '$1'"
  else
    echo "FAIL  '$1': got $got, want $want"
    fails=$((fails + 1))
  fi
}

expect minor "feat: add redirects"
expect minor "feat(dns): add Hetzner zones"
expect major "feat!: drop the old API"
expect major "feat(api)!: rename routes"
expect major "fix!: change the default port"
expect major "refactor!: remove a flag"
expect major "feat: new config" $'Details.\n\nBREAKING CHANGE: the env file moved'
expect major "fix: x" "BREAKING-CHANGE: y"
expect patch "fix: keep TTL on update"
expect patch "fix(web/sites): typo in label"
expect patch "perf: cache zone list"
expect none "docs: explain releases"
expect none "chore(deps): bump golang.org/x/net"
expect none "chore(deps-dev): bump @types/node in /web"
expect none "ci: run on release"
expect none "refactor: split store"
expect none "test: cover rollback"
expect none "build: pin node"
expect none "docs: mention BREAKING CHANGE: in text" "no breaking footer here"

expect invalid "Add installer, setup wizard and redirects"
expect invalid "Merge pull request #42 from danielgtmn/ci/path-filter"
expect invalid "Build(deps): bump golang.org/x/net"
expect invalid "CI: run only the checks affected by a pull request"
expect invalid "feat:missing space"
expect invalid "feat: "
expect invalid "feat : space before colon"
expect invalid "feature: unknown type"
expect invalid "style: unknown type here"
expect invalid "feat(): empty scope"
expect invalid "feat(a b): space in scope"
expect invalid "feat!!: two bangs"
expect invalid " feat: leading space"
expect invalid ""

if scripts/commit-type.sh >/dev/null 2>&1; then
  echo "FAIL  no arguments should fail"
  fails=$((fails + 1))
else
  echo "ok    no arguments fails"
fi

echo
if [ "$fails" -gt 0 ]; then
  echo "$fails test(s) failed"
  exit 1
fi
echo "all tests passed"

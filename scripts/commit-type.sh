#!/usr/bin/env bash
# Checks a commit message or pull request title against the Conventional
# Commits format the release version is computed from, and prints the version
# bump it causes. Used by the "commits" job in .github/workflows/commits.yml; tested
# by scripts/commit-type_test.sh.
#
#   scripts/commit-type.sh <subject> [<body>]
#
# Prints major, minor, patch or none and exits 0, or explains the format on
# stderr and exits 1. The format is "<type>[(<scope>)][!]: <description>":
#   feat                          minor (a new feature)
#   fix, perf                     patch
#   docs, chore, ci, refactor,    none
#   test, build
#   "!" before the colon, or a line "BREAKING CHANGE: ..." in the body: major
set -euo pipefail

types='feat|fix|docs|chore|ci|refactor|test|perf|build'
re="^($types)(\\([A-Za-z0-9._/-]+\\))?(!)?: [^[:space:]]"

[ $# -ge 1 ] && [ $# -le 2 ] || { echo "usage: scripts/commit-type.sh <subject> [<body>]" >&2; exit 2; }
subject=$1
body=${2:-}

if ! [[ $subject =~ $re ]]; then
  cat >&2 <<EOF
"$subject" does not follow the format "<type>(<scope>): <description>"
(scope optional, "!" before the colon for a breaking change), e.g.
"feat: add a passkey plugin" or "fix(password): keep the session on change".
Types: ${types//|/, }.
EOF
  exit 1
fi

type=${BASH_REMATCH[1]}
if [ -n "${BASH_REMATCH[3]}" ] || grep -qE '^BREAKING[ -]CHANGE: ' <<<"$body"; then
  echo major
elif [ "$type" = feat ]; then
  echo minor
elif [ "$type" = fix ] || [ "$type" = perf ]; then
  echo patch
else
  echo none
fi

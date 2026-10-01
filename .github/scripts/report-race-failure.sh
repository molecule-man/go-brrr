#!/usr/bin/env bash
set -euo pipefail

title="race: go test -race failed"

body=$(cat <<EOF2
The nightly race detector run failed.

- Commit: $SHA
- Run: $RUN_URL

The race report is in the log of the \`race\` job.

To reproduce:

1. Run \`make lib/libbrotli_cref.a\`.
2. Run \`go test -race ./...\` on a machine with 2 or more CPUs.
EOF2
)

number=$(gh issue list --repo "$REPO" --state open --limit 100 --json number,title |
	jq -r --arg t "$title" 'map(select(.title == $t)) | .[0].number // empty')

if [ -n "$number" ]; then
	gh issue comment "$number" --repo "$REPO" --body "$body"
else
	gh issue create --repo "$REPO" --title "$title" --body "$body"
fi

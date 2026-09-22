#!/usr/bin/env bash
# Open an issue for a failed fuzz target. Comment on the open issue if one exists.
set -euo pipefail

title="fuzz: $TARGET failed"

body=$(cat <<EOF
Nightly fuzzing failed.

- Target: \`$TARGET\`
- Commit: $SHA
- Run: $RUN_URL

The failing input is in the \`crashers-$TARGET\` artifact of that run.

To reproduce:

1. Download the artifact. Unpack it into \`testdata/fuzz/\`.
2. Run \`make lib/libbrotli_cref.a\`.
3. Run \`go test -run='^$TARGET\$' .\`
4. Minimize the input. Decide if it becomes a regression test.
EOF
)

number=$(gh issue list --repo "$REPO" --state open --limit 100 --json number,title |
	jq -r --arg t "$title" 'map(select(.title == $t)) | .[0].number // empty')

if [ -n "$number" ]; then
	gh issue comment "$number" --repo "$REPO" --body "$body"
else
	gh issue create --repo "$REPO" --title "$title" --body "$body"
fi

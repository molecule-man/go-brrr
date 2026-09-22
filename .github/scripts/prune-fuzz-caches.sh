#!/usr/bin/env bash
set -euo pipefail

prefix="fuzz-$TARGET-"
keep="$prefix$GITHUB_RUN_ID"

gh cache list --repo "$REPO" --key "$prefix" --limit 100 --json key --jq '.[].key' |
	while read -r key; do
		if [ "$key" != "$keep" ]; then
			gh cache delete "$key" --repo "$REPO"
		fi
	done

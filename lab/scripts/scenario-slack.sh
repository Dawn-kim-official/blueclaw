#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
host_mode="$2"

test -n "$sudo_password"
test "$host_mode" = "single-mac" || test "$host_mode" = "dual-mac"
test -n "${SLACK_BOT_TOKEN:-}"

curl --silent --show-error https://slack.com/api/auth.test \
  -H "Authorization: Bearer $SLACK_BOT_TOKEN" | grep -q '"ok":true'

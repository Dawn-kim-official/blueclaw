#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mattermost_listen_address="$2"
mount_directory_path="$3"

test -n "$sudo_password"
test -n "$mattermost_listen_address"
test -d "$mount_directory_path"

curl --silent --show-error "http://$mattermost_listen_address/api/v4/system/ping" >/dev/null

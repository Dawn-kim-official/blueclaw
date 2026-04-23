#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
host_mode="$2"
companion_listen_address="$3"
callback_base_url="$4"

test -n "$sudo_password"
test "$host_mode" = "single-mac" || test "$host_mode" = "dual-mac"
test -n "$companion_listen_address"
test -n "$callback_base_url"

curl --silent --show-error "$callback_base_url" >/dev/null

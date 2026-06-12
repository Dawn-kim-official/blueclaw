#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mattermost_listen_address="$2"
mount_directory_path="$3"

test -n "$sudo_password"
test -n "$mattermost_listen_address"
test -d "$mount_directory_path"

blueclaw_request() {
  local phase_name="$1"
  local method="$2"
  local url="$3"
  local body="${4:-}"
  local response_file
  local status
  local curl_status
  response_file="$(mktemp)"
  if [ -n "$body" ]; then
    status="$(curl --silent --show-error --output "$response_file" --write-out "%{http_code}" \
      -X "$method" -H "Content-Type: application/json" -d "$body" "$url")" || curl_status="$?"
  else
    status="$(curl --silent --show-error --output "$response_file" --write-out "%{http_code}" \
      -X "$method" "$url")" || curl_status="$?"
  fi
  if [ "${curl_status:-0}" != "0" ]; then
    echo "Blueclaw API curl failure during $phase_name: $method $url (curl exit ${curl_status:-0})" >&2
    cat "$response_file" >&2 || true
    rm -f "$response_file"
    return "${curl_status:-1}"
  fi
  if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
    echo "Blueclaw API failure during $phase_name: $method $url returned HTTP $status" >&2
    cat "$response_file" >&2 || true
    echo >&2
    rm -f "$response_file"
    return 22
  fi
  cat "$response_file"
  rm -f "$response_file"
}

wait_for_blueclaw_policy() {
  for _ in $(seq 1 120); do
    if blueclaw_request "wait for policy API" GET http://127.0.0.1:8080/admin/api/policy >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Blueclaw policy API did not become available after restart" >&2
  return 1
}

policy_person_count() {
  blueclaw_request "policy person count" GET http://127.0.0.1:8080/admin/api/policy |
    jq '.people | length'
}

restart_blueclaw() {
  if [ "$(id -u)" = "0" ]; then
    systemctl restart blueclaw
    return 0
  fi
  printf '%s\n' "$sudo_password" | sudo -S systemctl restart blueclaw
}

echo "recording Blueclaw policy person count before restart"
before_restart_count="$(policy_person_count)"
test -n "$before_restart_count"

echo "restarting Blueclaw service"
restart_blueclaw

echo "checking Blueclaw policy person count immediately after restart before users-sync"
wait_for_blueclaw_policy
after_restart_count="$(policy_person_count)"
test -n "$after_restart_count"

if [ "$after_restart_count" != "$before_restart_count" ]; then
  echo "Blueclaw policy person count changed across restart: before=$before_restart_count after=$after_restart_count" >&2
  exit 1
fi

echo "restart-policy-survival: ok"

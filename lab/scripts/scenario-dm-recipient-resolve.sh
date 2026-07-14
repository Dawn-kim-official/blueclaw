#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mattermost_listen_address="$2"
mount_directory_path="$3"

test -n "$sudo_password"
test -n "$mattermost_listen_address"
test -d "$mount_directory_path"

timestamp="$(date +%s)"
test_email="dm-recipient-resolve-$timestamp@internkim.test"
test_username="dmrecipientresolve$timestamp"
test_password="ResolvePass!$timestamp-InternKim-Mattermost"
test_display_name="DM Recipient Resolve $timestamp"
recipient_hint="Recipient Resolve $timestamp"
test_marker="InternKim DM recipient resolve verification $timestamp"
admin_token=""
test_user_identifier=""
direct_channel_identifier=""
test_post_identifier=""
test_started_at="$(date +%s%3N)"

echo "capabilityd recipient route is not called directly from this shell scenario; asserting the Blueclaw recipient resolver endpoint."

phase() {
  echo "$1"
}

mattermost_request() {
  local phase_name="$1"
  local method="$2"
  local url="$3"
  local token="${4:-}"
  local body="${5:-}"
  local response_file
  local status
  local curl_status
  response_file="$(mktemp)"
  if [ -n "$body" ]; then
    if [ -n "$token" ]; then
      status="$(curl --silent --show-error --output "$response_file" --write-out "%{http_code}" \
        -X "$method" -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
        -d "$body" "$url")" || curl_status="$?"
    else
      status="$(curl --silent --show-error --output "$response_file" --write-out "%{http_code}" \
        -X "$method" -H "Content-Type: application/json" \
        -d "$body" "$url")" || curl_status="$?"
    fi
  else
    if [ -n "$token" ]; then
      status="$(curl --silent --show-error --output "$response_file" --write-out "%{http_code}" \
        -X "$method" -H "Authorization: Bearer $token" "$url")" || curl_status="$?"
    else
      status="$(curl --silent --show-error --output "$response_file" --write-out "%{http_code}" \
        -X "$method" "$url")" || curl_status="$?"
    fi
  fi
  if [ "${curl_status:-0}" != "0" ]; then
    echo "Mattermost API curl failure during $phase_name: $method $url (curl exit ${curl_status:-0})" >&2
    cat "$response_file" >&2 || true
    rm -f "$response_file"
    return "${curl_status:-1}"
  fi
  if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
    echo "Mattermost API failure during $phase_name: $method $url returned HTTP $status" >&2
    cat "$response_file" >&2 || true
    echo >&2
    rm -f "$response_file"
    return 22
  fi
  cat "$response_file"
  rm -f "$response_file"
}

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

wait_for_blueclaw_health() {
  for _ in $(seq 1 120); do
    if curl --silent --show-error --fail --max-time 5 http://127.0.0.1:8080/admin/api/health |
      jq -e '.status == "ok"' >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "Blueclaw API did not become healthy before DM recipient resolve verification" >&2
  return 1
}

read_root_secret() {
  local path="$1"
  printf '%s\n' "$sudo_password" | sudo -S -p '' cat "$path"
}

delete_post() {
  local post_identifier="$1"
  if [ -z "$post_identifier" ] || [ "$post_identifier" = "null" ] || [ -z "$admin_token" ]; then
    return 0
  fi
  curl --silent --show-error -X DELETE -H "Authorization: Bearer $admin_token" \
    "http://$mattermost_listen_address/api/v4/posts/$post_identifier" >/dev/null || true
}

delete_test_posts() {
  if [ -z "$direct_channel_identifier" ] || [ -z "$admin_token" ]; then
    return 0
  fi
  mattermost_request "cleanup enumerate DM recipient resolve posts" GET "http://$mattermost_listen_address/api/v4/channels/$direct_channel_identifier/posts?per_page=100" "$admin_token" |
    jq -r --arg marker "$test_marker" --argjson test_started_at "$test_started_at" \
      '.posts[] | select(((.message // "") | contains($marker)) or (.create_at >= $test_started_at)) | .id' |
    while read -r post_identifier; do
      delete_post "$post_identifier"
    done || echo "cleanup warning: failed to enumerate DM recipient resolve posts" >&2
}

delete_test_user() {
  if [ -z "$test_user_identifier" ] || [ "$test_user_identifier" = "null" ] || [ -z "$admin_token" ]; then
    return 0
  fi
  curl --fail --silent --show-error -X DELETE -H "Authorization: Bearer $admin_token" \
    "http://$mattermost_listen_address/api/v4/users/$test_user_identifier?permanent=true" >/dev/null || \
    curl --fail --silent --show-error -X DELETE -H "Authorization: Bearer $admin_token" \
      "http://$mattermost_listen_address/api/v4/users/$test_user_identifier" >/dev/null || \
    echo "cleanup warning: failed to delete Mattermost user $test_user_identifier" >&2
}

cleanup() {
  delete_test_posts
  delete_test_user
  curl --silent --show-error -X DELETE "http://127.0.0.1:8080/admin/api/people?email=$test_email" >/dev/null || true
}
trap cleanup EXIT

phase "check Mattermost"
curl --silent --show-error "http://$mattermost_listen_address/api/v4/system/ping" >/dev/null

phase "wait for Blueclaw"
wait_for_blueclaw_health

phase "admin login"
admin_password="$(read_root_secret /root/.internkim/secrets/mm-admin-pass)"
login_headers="$(mktemp)"
login_body="$(jq -cn --arg login_id admin --arg password "$admin_password" '{login_id:$login_id,password:$password}')"
curl --silent --show-error --fail -D "$login_headers" -o /tmp/internkim-dm-recipient-resolve-admin-login.json \
  -H "Content-Type: application/json" \
  -d "$login_body" \
  "http://$mattermost_listen_address/api/v4/users/login" >/dev/null
admin_token="$(awk 'tolower($1) == "token:" {print $2}' "$login_headers" | tr -d '\r')"
test -n "$admin_token"

phase "bot lookup"
mattermost_token="$(read_root_secret /root/.internkim/secrets/mattermost-bot-token)"
bot_user_identifier="$(mattermost_request "bot lookup" GET "http://$mattermost_listen_address/api/v4/users/me" "$mattermost_token" | jq -r '.id // empty')"
test -n "$bot_user_identifier"

phase "create test Mattermost user"
user_body="$(jq -cn \
  --arg email "$test_email" \
  --arg username "$test_username" \
  --arg password "$test_password" \
  --arg nickname "$test_display_name" \
  '{email:$email,username:$username,password:$password,nickname:$nickname}')"
test_user_identifier="$(mattermost_request "create DM recipient resolve user" POST "http://$mattermost_listen_address/api/v4/users" "$admin_token" "$user_body" | jq -r '.id // empty')"
test -n "$test_user_identifier"

phase "invite policy person"
blueclaw_request "invite DM recipient resolve person" POST http://127.0.0.1:8080/admin/api/people/invite \
  "$(jq -cn \
    --arg personID "$test_user_identifier" \
    --arg email "$test_email" \
    --arg displayName "$test_display_name" \
    '{personID:$personID,email:$email,displayName:$displayName}')" >/dev/null

phase "create direct channel"
user_login_headers="$(mktemp)"
user_login_body="$(jq -cn --arg login_id "$test_username" --arg password "$test_password" '{login_id:$login_id,password:$password}')"
curl --silent --show-error --fail -D "$user_login_headers" -o /tmp/internkim-dm-recipient-resolve-user-login.json \
  -H "Content-Type: application/json" \
  -d "$user_login_body" \
  "http://$mattermost_listen_address/api/v4/users/login" >/dev/null
test_user_token="$(awk 'tolower($1) == "token:" {print $2}' "$user_login_headers" | tr -d '\r')"
test -n "$test_user_token"
direct_channel_identifier="$(mattermost_request "create direct channel" POST "http://$mattermost_listen_address/api/v4/channels/direct" "$test_user_token" \
  "$(jq -cn --arg testUserIdentifier "$test_user_identifier" --arg botUserIdentifier "$bot_user_identifier" '[$testUserIdentifier,$botUserIdentifier]')" | jq -r '.id // empty')"
test -n "$direct_channel_identifier"

phase "post identity trigger"
test_post_identifier="$(mattermost_request "post identity trigger" POST "http://$mattermost_listen_address/api/v4/posts" "$test_user_token" \
  "$(jq -cn --arg channelIdentifier "$direct_channel_identifier" --arg message "$test_marker" '{channel_id:$channelIdentifier,message:$message}')" | jq -r '.id // empty')"
test -n "$test_post_identifier"

phase "resolve recipient"
resolution_body="$(jq -cn --arg platform mattermost --arg hint "$recipient_hint" '{platform:$platform,hint:$hint}')"
for _ in $(seq 1 60); do
  resolution_response="$(blueclaw_request "resolve DM recipient" POST http://127.0.0.1:8080/admin/api/identity/resolve-recipient "$resolution_body")"
  if printf '%s' "$resolution_response" |
    jq -e \
      --arg personIdentifier "$test_user_identifier" \
      --arg externalUserIdentifier "$test_user_identifier" \
      '.status == "resolved" and .recipient.personID == $personIdentifier and .recipient.externalUserID == $externalUserIdentifier' >/dev/null; then
    echo "dm-recipient-resolve: ok"
    exit 0
  fi
  sleep 1
done

echo "DM recipient resolve did not return resolved status for $test_display_name" >&2
printf '%s\n' "$resolution_response" >&2
exit 1

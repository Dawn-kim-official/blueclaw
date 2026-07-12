#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mattermost_listen_address="$2"
mount_directory_path="$3"
evidence_directory_path="${4:-$mount_directory_path/.artifacts/artifact-delivery}"
blueclaw_url=http://127.0.0.1:8080

blueclaw_test_model=google/gemini-3.1-flash-lite
task_run_id=

test -n "$sudo_password"
test -n "$mattermost_listen_address"
test -d "$mount_directory_path"
mkdir -p "$evidence_directory_path"

cleanup() {
  if [ -z "$task_run_id" ]; then
    return
  fi
  curl -fsS -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg task "$task_run_id" '{taskRunID:$task,viewerIsAdmin:true}')" \
    "$blueclaw_url/admin/api/task/delete" >/dev/null 2>&1 || true
}
trap cleanup EXIT

runtime_configuration=/root/.blueclaw/config/runtime.json
for model_field in model lowModel mediumModel highModel xhighModel maxModel codingModel visionModel; do
  configured_model="$(printf '%s\n' "$sudo_password" | sudo -S jq -er --arg field "$model_field" '.languageModel.capability[$field]' "$runtime_configuration" 2>/dev/null)"
  if [ "$configured_model" != "$blueclaw_test_model" ]; then
    echo "artifact-delivery: $model_field is $configured_model, expected $blueclaw_test_model" >&2
    exit 1
  fi
done

requester_person_id="$(curl -fsS "$blueclaw_url/admin/api/policy" | jq -er '.people[0].personID')"
task_response="$(curl -fsS --max-time 900 -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg person "$requester_person_id" '{requesterPersonID:$person,requesterName:"Artifact Delivery Regression",conversationID:"regression:artifact-delivery",prompt:"Create a small valid HTML file named artifact-delivery.html containing the heading Local Fleet Artifact Delivery Test, then attach and deliver the HTML file to me.",pinnedSkillNames:["presentation"]}')" \
  "$blueclaw_url/admin/api/task/run")"
task_run_id="$(jq -er '.taskRun.taskRunID' <<<"$task_response")"
printf '%s\n' "$task_response" >"$evidence_directory_path/task-run.json"

jq -e '.taskRun.status == "completed"' <<<"$task_response" >/dev/null
jq -e '[.attachments[]? | select((.filename // "") | endswith(".html"))] | length > 0' <<<"$task_response" >/dev/null

task_detail="$(curl -fsS "$blueclaw_url/admin/api/task/detail?taskRunID=$task_run_id")"
printf '%s\n' "$task_detail" >"$evidence_directory_path/task-detail.json"
jq -e '[.taskEvents[]? | select(.name == "tool.file.deliver.result") | .body | fromjson? | .attachments[]? | select((.filename // "") | endswith(".html"))] | length > 0' <<<"$task_detail" >/dev/null

html_filename="$(jq -er '.attachments[] | select((.filename // "") | endswith(".html")) | .filename' <<<"$task_response" | head -n 1)"
echo "artifact-delivery: ok taskRunID=$task_run_id attachment=$html_filename model=$blueclaw_test_model"

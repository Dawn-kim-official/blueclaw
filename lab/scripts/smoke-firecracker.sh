#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
firecracker_binary_path="$2"
kernel_image_path="$3"
rootfs_image_path="$4"
workspace_image_path="$5"
vsock_cid="$6"
mount_directory_path="$7"

test -c /dev/kvm
test -d "$mount_directory_path"
test -f "$kernel_image_path"
test -f "$rootfs_image_path"
test -f "$workspace_image_path"

if command -v "$firecracker_binary_path" >/dev/null 2>&1; then
  firecracker_command="$firecracker_binary_path"
else
  firecracker_command="$(command -v firecracker)"
fi

api_socket_path="/tmp/blueclaw-lab-firecracker.socket"
vsock_socket_path="/tmp/blueclaw-lab-firecracker.vsock"
firecracker_log_path="/tmp/blueclaw-lab-firecracker.log"
rm -f "$api_socket_path" "$vsock_socket_path" "$firecracker_log_path"

printf '%s\n' "$sudo_password" | sudo -S "$firecracker_command" --api-sock "$api_socket_path" >"$firecracker_log_path" 2>&1 &
firecracker_process_id="$!"

cleanup() {
  if kill -0 "$firecracker_process_id" >/dev/null 2>&1; then
    printf '%s\n' "$sudo_password" | sudo -S kill "$firecracker_process_id"
    wait "$firecracker_process_id" || true
  fi
  rm -f "$api_socket_path" "$vsock_socket_path"
}

trap cleanup EXIT

for _ in $(seq 1 50); do
  if [ -S "$api_socket_path" ]; then
    break
  fi
  sleep 0.1
done

test -S "$api_socket_path"

curl --silent --show-error --unix-socket "$api_socket_path" -X PUT 'http://localhost/machine-config' \
  -H 'Content-Type: application/json' \
  -d '{"vcpu_count":1,"mem_size_mib":512,"smt":false}' >/dev/null

curl --silent --show-error --unix-socket "$api_socket_path" -X PUT 'http://localhost/boot-source' \
  -H 'Content-Type: application/json' \
  -d "{\"kernel_image_path\":\"$kernel_image_path\",\"boot_args\":\"console=ttyS0 reboot=k panic=1 pci=off\"}" >/dev/null

curl --silent --show-error --unix-socket "$api_socket_path" -X PUT 'http://localhost/drives/rootfs' \
  -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"rootfs\",\"path_on_host\":\"$rootfs_image_path\",\"is_root_device\":true,\"is_read_only\":true}" >/dev/null

curl --silent --show-error --unix-socket "$api_socket_path" -X PUT 'http://localhost/drives/workspace' \
  -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"workspace\",\"path_on_host\":\"$workspace_image_path\",\"is_root_device\":false,\"is_read_only\":false}" >/dev/null

curl --silent --show-error --unix-socket "$api_socket_path" -X PUT 'http://localhost/vsock' \
  -H 'Content-Type: application/json' \
  -d "{\"guest_cid\":$vsock_cid,\"uds_path\":\"$vsock_socket_path\"}" >/dev/null

curl --silent --show-error --unix-socket "$api_socket_path" -X PUT 'http://localhost/actions' \
  -H 'Content-Type: application/json' \
  -d '{"action_type":"InstanceStart"}' >/dev/null

sleep 2
kill -0 "$firecracker_process_id"

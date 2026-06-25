#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mount_directory_path="$2"

ensure_shared_workspace() {
  if [ -z "$mount_directory_path" ]; then
    return 0
  fi

  printf '%s\n' "$sudo_password" | sudo -S mkdir -p "$mount_directory_path"
  if [ -d "$mount_directory_path/workspace" ]; then
    return 0
  fi
  if mount | grep -Fq "com.apple.virtio-fs.automount on $mount_directory_path "; then
    return 0
  fi

  printf '%s\n' "$sudo_password" | sudo -S mount -t virtiofs com.apple.virtio-fs.automount "$mount_directory_path"
}

printf '%s\n' "$sudo_password" | sudo -S apt-get update
printf '%s\n' "$sudo_password" | sudo -S apt-get install -y openssh-server curl jq iproute2
ensure_shared_workspace
printf '%s\n' "$sudo_password" | sudo -S systemctl enable ssh
printf '%s\n' "$sudo_password" | sudo -S systemctl start ssh

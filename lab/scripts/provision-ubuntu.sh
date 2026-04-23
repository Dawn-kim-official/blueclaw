#!/usr/bin/env bash
set -euo pipefail

sudo_password="$1"
mount_directory_path="$2"

printf '%s\n' "$sudo_password" | sudo -S apt-get update
printf '%s\n' "$sudo_password" | sudo -S apt-get install -y openssh-server curl jq iproute2
printf '%s\n' "$sudo_password" | sudo -S mkdir -p "$mount_directory_path"
if ! mount | grep -q "com.apple.virtio-fs.automount on $mount_directory_path "; then
  printf '%s\n' "$sudo_password" | sudo -S mount -t virtiofs com.apple.virtio-fs.automount "$mount_directory_path"
fi
printf '%s\n' "$sudo_password" | sudo -S systemctl enable ssh
printf '%s\n' "$sudo_password" | sudo -S systemctl start ssh

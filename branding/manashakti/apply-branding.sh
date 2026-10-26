#!/usr/bin/env bash
# Pre-build branding for the Manashakti Support Windows client.
# Run from the repository root before build.py. Only touches build inputs
# (icons, version-resource strings); the app name, server and key come from
# the signed custom.txt added to the dist folder after the build.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
company="Manashakti Research Centre"
product="Manashakti Support"

cp "$here/icon.ico" res/icon.ico
cp "$here/tray-icon.ico" res/tray-icon.ico
cp "$here/icon.ico" flutter/windows/runner/resources/app_icon.ico
cp "$here/icon.png" res/icon.png

# Version resource shown in file Properties / Task Manager.
# LegalCopyright is left as-is: AGPL requires keeping upstream copyright notices.
rc=flutter/windows/runner/Runner.rc
sed -i \
  -e "s/\"CompanyName\", \"Purslane Tech Pte\. Ltd\.\"/\"CompanyName\", \"$company\"/" \
  -e "s/\"FileDescription\", \"RustDesk Remote Desktop\"/\"FileDescription\", \"$product\"/" \
  -e "s/\"ProductName\", \"RustDesk\"/\"ProductName\", \"$product\"/" \
  "$rc"
sed -i \
  -e "/^\[package.metadata.winres\]/,/^\[/{s/^ProductName = \"RustDesk\"/ProductName = \"$product\"/;s/^FileDescription = \"RustDesk Remote Desktop\"/FileDescription = \"$product\"/}" \
  Cargo.toml

# Fail loudly if upstream changed these strings and nothing was replaced.
grep -q "\"ProductName\", \"$product\"" "$rc" || { echo "branding: Runner.rc not updated"; exit 1; }
grep -q "^ProductName = \"$product\"" Cargo.toml || { echo "branding: Cargo.toml not updated"; exit 1; }
echo "branding: applied"

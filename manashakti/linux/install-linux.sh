#!/usr/bin/env bash
# Sets up a Linux PC for Manashakti Support (unattended access) using an invite link.
#
#   curl -fsSLO https://raw.githubusercontent.com/mindgym-lonavala/rustdesk/master/manashakti/linux/install-linux.sh
#   sudo bash install-linux.sh 'https://support.manashakti.org:8443/i/<invite code>'
#
# Create the invite on /invites with "Offer allow help any time" ticked. The script installs the
# official RustDesk 1.5.1 package (checksum-verified), points it at the Manashakti server, sets a
# random permanent password and adds the PC to the technician's address book. The app keeps the
# name "RustDesk" on Linux (its service and paths are tied to that name).
# Supported: Debian/Ubuntu/Mint (apt) and Fedora/RHEL (dnf), x86_64.
set -euo pipefail

SERVER="support.manashakti.org"
API="https://support.manashakti.org:8443"
KEY="G1eThlVaDig57vVB3ZDgNH9e5Sa6yb795XHQ+YGp8o4="
VERSION="1.5.1"
DEB_SHA256="11e0af5f25e591358fa5190db96a808e14289dbe32b66e5cca8e230e28e8d479"
RPM_SHA256="ec7c79e5020911b05b66a57219132aab62a894797ddd39619cb9bb3b61fb11ba"

die() { echo "Error: $*" >&2; exit 1; }
trap 'echo "Error: setup stopped unexpectedly at line $LINENO of $0" >&2' ERR
step() { echo "  - $*..."; }

[[ $EUID -eq 0 ]] || die "run with sudo: sudo bash $0 '<invite link>'"
[[ $(uname -m) == x86_64 ]] || die "only x86_64 is supported"
[[ $# -eq 1 ]] || die "usage: sudo bash $0 '<invite link>'"
LINK="${1//[[:space:]]/}"   # tolerate a line break or spaces pasted with the link
[[ $LINK =~ ^${API//./\\.}/i/([A-Za-z0-9_-]{43})$ ]] || die "that is not a Manashakti Support invite link"
TOKEN="${BASH_REMATCH[1]}"
command -v curl >/dev/null || die "curl is required (apt-get install curl)"

echo "Manashakti IT Support - Linux setup"
info=$(curl -fsS "$API/api/invite/$TOKEN") || die "invite not valid (expired, cancelled or already used?)"
grep -q '"allow_setup":true' <<<"$info" || die "this invite does not offer permanent access, or was already used"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if command -v apt-get >/dev/null; then
    pkg="rustdesk-$VERSION-x86_64.deb"; sum="$DEB_SHA256"
elif command -v dnf >/dev/null; then
    pkg="rustdesk-$VERSION-0.x86_64.rpm"; sum="$RPM_SHA256"
else
    die "unsupported distribution (needs apt-get or dnf)"
fi

if [[ $(rustdesk --version 2>/dev/null || true) == "$VERSION" ]]; then
    step "RustDesk $VERSION is already installed; using it"
else
    step "Downloading RustDesk $VERSION"
    curl -fsSL -o "$tmp/$pkg" "https://github.com/rustdesk/rustdesk/releases/download/$VERSION/$pkg"
    echo "$sum  $tmp/$pkg" | sha256sum -c --quiet - || die "download checksum mismatch"
    step "Installing"
    if [[ $pkg == *.deb ]]; then
        if [[ -n $(dpkg --audit 2>/dev/null) ]]; then
            die "an earlier software installation on this PC was interrupted. Run:
    sudo dpkg --configure -a && sudo apt-get -f install -y
then run this script again with the same invite link."
        fi
        DEBIAN_FRONTEND=noninteractive apt-get install -y "$tmp/$pkg" >/dev/null \
            || die "apt-get could not install RustDesk (see the message above); fix that and run this script again with the same invite link"
    else
        dnf install -y "$tmp/$pkg" >/dev/null
    fi
fi

step "Starting the RustDesk service"
systemctl enable --now rustdesk >/dev/null 2>&1 || die "could not start rustdesk.service"
sleep 5

step "Pointing it at the Manashakti Support server"
rustdesk --option custom-rendezvous-server "$SERVER" >/dev/null
rustdesk --option relay-server "$SERVER" >/dev/null
rustdesk --option key "$KEY" >/dev/null
rustdesk --option api-server "$API" >/dev/null
rustdesk --option verification-method use-both-passwords >/dev/null
[[ $(rustdesk --option custom-rendezvous-server 2>/dev/null | tail -n1) == "$SERVER" ]] || die "settings were not applied (is rustdesk.service running?)"

step "Setting a random permanent password"
# Read a finite chunk at a time: "tr </dev/urandom | head" gets SIGPIPE, which pipefail turns into an exit.
PW=""
while [[ ${#PW} -lt 24 ]]; do
    PW+=$(head -c 64 /dev/urandom | LC_ALL=C tr -dc 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789')
done
PW=${PW:0:24}
out=$(rustdesk --password "$PW" 2>&1 || true)
[[ $out == *"Done!"* ]] || die "could not set the password: $out"

systemctl restart rustdesk
ID=""
for _ in $(seq 1 30); do
    ID=$(rustdesk --get-id 2>/dev/null | grep -E '^[0-9]{6,12}$' | tail -n1 || true)
    [[ -n $ID ]] && break
    sleep 2
done
[[ -n $ID ]] || die "RustDesk did not report its ID"

step "Adding this computer to Manashakti IT's list"
host=$(hostname | tr -cd 'A-Za-z0-9._-' | cut -c1-60)
user=$(printf '%s' "${SUDO_USER:-root}" | tr -cd 'A-Za-z0-9._-' | cut -c1-60)
body=$(printf '{"mode":"setup","id":"%s","password":"%s","hostname":"%s","username":"%s","platform":"Linux"}' "$ID" "$PW" "$host" "$user")
if ! resp=$(curl -fsS -X POST -H 'Content-Type: application/json' -d "$body" "$API/api/invite/$TOKEN/enroll" 2>&1); then
    echo "Enrolment failed: $resp" >&2
    echo "Keep these to connect manually: ID $ID  password $PW" >&2
    exit 1
fi

echo
echo "Done. ID $ID is now in the technician's address book."

# Unattended control needs an X11 session; RustDesk cannot control the Wayland login screen.
if loginctl list-sessions --no-legend 2>/dev/null | awk '{print $1}' | xargs -r -I{} loginctl show-session {} -p Type --value 2>/dev/null | grep -qx wayland \
   || grep -qsE '^\s*WaylandEnable\s*=\s*true' /etc/gdm3/custom.conf /etc/gdm/custom.conf; then
    echo
    echo "Note: this PC uses Wayland. For reliable unattended access, use an X11 (Xorg) session:"
    echo "  GNOME/GDM: set WaylandEnable=false in /etc/gdm3/custom.conf (or /etc/gdm/custom.conf) and reboot."
fi

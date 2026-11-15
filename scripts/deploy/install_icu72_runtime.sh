#!/usr/bin/env bash
# Provision the canonical BrainRust ICU 72 runtime on Ubuntu 22.04 hosts.
# Ubuntu Jammy ships ICU 70; the versioned Debian Bookworm runtime library can
# coexist with it and is the ABI used by the pinned BrainRust release.
set -euo pipefail

VERSION='72.1-3+deb12u1'
DEB="libicu72_${VERSION}_amd64.deb"
URL="https://deb.debian.org/debian/pool/main/i/icu/${DEB}"
SHA256='f7f6f99c6d7b025914df2447fc93e11d22c44c0c8bdd8b6f36691c9e7ddcef88'

installed="$(dpkg-query -W -f='${Version}' libicu72 2>/dev/null || true)"
if [ -n "$installed" ] && dpkg --compare-versions "$installed" ge "$VERSION"; then
    echo "✅ libicu72 $installed is already installed"
    exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
deb="$tmp/$DEB"
curl -fsSL --retry 3 --retry-delay 2 "$URL" -o "$deb"
printf '%s  %s\n' "$SHA256" "$deb" | sha256sum --check --status || {
    echo "ICU runtime package checksum mismatch" >&2
    exit 1
}

dpkg-deb -f "$deb" Package Version Architecture Depends
if [ "$(dpkg --print-architecture)" != amd64 ]; then
    echo "This pinned package is for amd64; provision the matching official libicu72 package for this architecture." >&2
    exit 2
fi

sudo dpkg -i "$deb"
sudo ldconfig
ldconfig -p | awk '$1 == "libicuuc.so.72" || $1 == "libicui18n.so.72" { found[$1] = 1 } END { exit !(found["libicuuc.so.72"] && found["libicui18n.so.72"]) }'
echo "✅ ICU 72 runtime libraries are registered with the dynamic loader"

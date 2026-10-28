#!/bin/sh
# Puts the pinned LadybugDB static library and its headers into internal/ladybug/lib and checks their sha256.
set -eu

version=0.21.2
lib=$(cd "$(dirname "$0")" && pwd)/lib

case "$(uname -s)/$(uname -m)" in
Linux/x86_64)
	archive=liblbug-static-linux-x86_64-compat.tar.gz
	archive_sha256=60147b2ae28c092d8f895f1c23913766be8df477c379c2bc57d86ecada0a6a3f
	liblbug_sha256=9100591f7cf1de0f565c96b55e3293998763260fc936bfbb9e304bef6f4ec2b8
	lbug_h_sha256=fec7bf52a59ab673cac1d74443a0bf88867395e673766f8190dcc608deb9c307
	lbug_hpp_sha256=3657d3cffd954728c30710206c84971231b1decde5f5835c22a576cf5de3704d
	;;
*)
	echo "no LadybugDB v$version static library is pinned for $(uname -s)/$(uname -m)" >&2
	exit 1
	;;
esac

pinned() {
	printf '%s  %s\n' \
		"$liblbug_sha256" "$lib/liblbug.a" \
		"$lbug_h_sha256" "$lib/lbug.h" \
		"$lbug_hpp_sha256" "$lib/lbug.hpp" | sha256sum -c --quiet --status
}

if [ -f "$lib/liblbug.a" ] && pinned; then
	exit 0
fi

download=$(mktemp -d)
trap 'rm -rf "$download"' EXIT
curl -fsSL -o "$download/$archive" "https://github.com/LadybugDB/ladybug/releases/download/v$version/$archive"
printf '%s  %s\n' "$archive_sha256" "$download/$archive" | sha256sum -c --quiet
mkdir -p "$lib"
tar -xzf "$download/$archive" -C "$lib" liblbug.a lbug.h lbug.hpp
pinned

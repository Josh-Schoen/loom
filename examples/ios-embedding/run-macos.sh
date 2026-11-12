#!/usr/bin/env bash
# Builds the example as a macOS command-line program against
# bin/LoomMobile.xcframework and runs it with Apple's on-device model.
# Exits non-zero unless every turn completed.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
example_dir="$repo_root/examples/ios-embedding"
framework_dir="$(find "$repo_root/bin/LoomMobile.xcframework" -maxdepth 1 -type d -name 'macos-*' | head -1)"
binary="$repo_root/bin/LoomMacDemo"

if [[ -z "$framework_dir" ]]; then
    echo "error: no macOS slice in bin/LoomMobile.xcframework; run 'just build-apple-framework macos' first" >&2
    exit 1
fi

xcrun --sdk macosx swiftc \
    -target "$(uname -m)-apple-macos26.0" \
    -F "$framework_dir" -framework LoomMobile -lresolv \
    "$example_dir"/Shared/*.swift "$example_dir/MacDemo/main.swift" \
    -o "$binary"

"$binary" 2>/dev/null

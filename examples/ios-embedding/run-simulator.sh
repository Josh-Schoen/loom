#!/usr/bin/env bash
# Builds the LoomDemo app against bin/LoomMobile.xcframework, runs it in the
# booted iOS simulator and prints what the app reported. Exits non-zero unless
# the app completed an agent turn.
#
# Usage: run-simulator.sh [minimum iOS version] [scripted|on-device]
#
# The model defaults to the scripted stand-in. "on-device" selects Apple's
# on-device model, which needs a simulator and host that can serve it.
set -euo pipefail

ios_version="${1:-16.0}"
model="${2:-scripted}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
example_dir="$repo_root/examples/ios-embedding"
framework_dir="$repo_root/bin/LoomMobile.xcframework/ios-arm64_x86_64-simulator"
app_dir="$repo_root/bin/LoomDemo.app"
bundle_id="com.teradata.loom.demo"
wait_seconds=60

if [[ ! -d "$framework_dir" ]]; then
    echo "error: $framework_dir not found; run 'just build-apple-framework' first" >&2
    exit 1
fi
if ! xcrun simctl list devices booted | grep -q "(Booted)"; then
    echo "error: no booted iOS simulator; boot one with 'xcrun simctl boot <device>'" >&2
    exit 1
fi

rm -rf "$app_dir"
mkdir -p "$app_dir"
cp "$example_dir/LoomDemo/Info.plist" "$app_dir/Info.plist"
/usr/libexec/PlistBuddy -c "Add :MinimumOSVersion string $ios_version" "$app_dir/Info.plist"

xcrun --sdk iphonesimulator swiftc \
    -parse-as-library \
    -target "$(uname -m)-apple-ios${ios_version}-simulator" \
    -F "$framework_dir" -framework LoomMobile -lresolv \
    "$example_dir"/Shared/*.swift "$example_dir/LoomDemo/main.swift" \
    -o "$app_dir/LoomDemo"
codesign --sign - --force "$app_dir" >/dev/null 2>&1

log="$(mktemp -t loom-demo)"
trap 'rm -f "$log"' EXIT
xcrun simctl install booted "$app_dir"
SIMCTL_CHILD_LOOM_DEMO_MODEL="$model" xcrun simctl launch --console-pty --terminate-running-process booted "$bundle_id" >"$log" 2>&1 &
launch_pid=$!

for ((i = 0; i < wait_seconds; i++)); do
    if grep -q "LOOM_DEMO_END" "$log"; then
        break
    fi
    sleep 1
done
xcrun simctl terminate booted "$bundle_id" >/dev/null 2>&1 || true
wait "$launch_pid" 2>/dev/null || true

sed -n '/LOOM_DEMO_BEGIN/,/LOOM_DEMO_END/p' "$log"
if ! grep -q "LOOM_DEMO_OK" "$log"; then
    echo "error: the demo did not complete an agent turn; full log follows" >&2
    cat "$log" >&2
    exit 1
fi

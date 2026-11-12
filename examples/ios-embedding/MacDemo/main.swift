// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import Foundation

// Runs the demo on this Mac with Apple's on-device model. The provider and
// the demo are the same Swift sources the iOS app uses.

guard #available(macOS 26.0, *) else {
    print("LOOM_DEMO_FAILED: the on-device model needs macOS 26 or later")
    exit(1)
}
if let reason = OnDeviceModel.unavailableReason {
    print("LOOM_DEMO_FAILED: the on-device model is unavailable: \(reason)")
    exit(1)
}

let dataDir = FileManager.default.temporaryDirectory
    .appendingPathComponent("loom-macos-demo-\(UUID().uuidString)")
defer { try? FileManager.default.removeItem(at: dataDir) }

let report = runDemo(model: OnDeviceModel(), dataDir: dataDir)
print(report)
exit(report.contains("LOOM_DEMO_OK") ? 0 : 1)

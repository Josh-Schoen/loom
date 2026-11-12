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

import LoomMobile
import SwiftUI

/// Picks the model for the demo. Set LOOM_DEMO_MODEL=on-device to use Apple's
/// on-device model; the default is the scripted stand-in, which needs nothing.
func demoModel() -> LoomMobileModelProviderProtocol? {
    guard ProcessInfo.processInfo.environment["LOOM_DEMO_MODEL"] == "on-device" else {
        return ScriptedModel()
    }
    guard #available(iOS 26.0, *), OnDeviceModel.unavailableReason == nil else {
        return nil
    }
    return OnDeviceModel()
}

func runDemo() -> String {
    guard let model = demoModel() else {
        return "LOOM_DEMO_FAILED: the on-device model is not available here"
    }
    let dataDir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("loom")
    return runDemo(model: model, dataDir: dataDir)
}

@main
struct LoomDemoApp: App {
    @State private var output = "running…"

    var body: some Scene {
        WindowGroup {
            ScrollView {
                Text(output)
                    .font(.system(size: 12, design: .monospaced))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding()
            }
            .task {
                let result = await Task.detached { runDemo() }.value
                output = result
                print("LOOM_DEMO_BEGIN\n\(result)\nLOOM_DEMO_END")
            }
        }
    }
}

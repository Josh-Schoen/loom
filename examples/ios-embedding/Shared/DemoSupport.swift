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
import LoomMobile

/// Encodes a JSON object for the engine.
func encode(_ object: Any) -> String {
    let data = try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    return String(data: data, encoding: .utf8)!
}

/// Decodes a JSON object received from the engine.
func decode(_ json: String?) -> [String: Any] {
    guard let data = json?.data(using: .utf8),
          let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
    else { return [:] }
    return object
}

/// A stand-in for a real model, used where no model is available. It asks for
/// `trail_lookup` once, then answers from the tool result. `OnDeviceModel` is
/// the real counterpart; the engine sees no difference between them.
final class ScriptedModel: NSObject, LoomMobileModelProviderProtocol {
    func providerName() -> String { "scripted" }
    func modelID() -> String { "scripted-1" }

    func chat(_ requestJSON: String?, error: NSErrorPointer) -> String {
        let messages = decode(requestJSON)["messages"] as? [[String: Any]] ?? []
        guard let last = messages.last else {
            return encode(["content": "no messages", "stop_reason": "end_turn"])
        }
        if last["role"] as? String == "tool" {
            if let failure = last["tool_error"] as? String, !failure.isEmpty {
                return encode(["content": "The lookup failed: \(failure)", "stop_reason": "end_turn"])
            }
            let result = last["tool_result"] ?? NSNull()
            return encode([
                "content": "Trail facts: \(encode(["result": result]))",
                "stop_reason": "end_turn",
                "usage": ["input_tokens": 40, "output_tokens": 12],
            ])
        }
        return encode([
            "stop_reason": "tool_use",
            "tool_calls": [[
                "id": "call_\(messages.count)",
                "name": "trail_lookup",
                "input": ["name": last["content"] as? String ?? ""],
            ]],
        ])
    }
}

/// A tool implemented by the app. A real app would query its own trail data.
final class TrailLookup: NSObject, LoomMobileHostToolProtocol {
    func toolName() -> String { "trail_lookup" }
    func toolDescription() -> String { "Look up pitch and aspect for a named ski trail." }
    func inputSchemaJSON() -> String {
        encode([
            "type": "object",
            "properties": ["name": ["type": "string", "description": "Trail name"]],
            "required": ["name"],
        ])
    }

    func execute(_ inputJSON: String?, error: NSErrorPointer) -> String {
        let name = decode(inputJSON)["name"] as? String ?? ""
        if name.contains("Closed Run") {
            // Setting the error reports a failed tool call to the model.
            error?.pointee = NSError(domain: "LoomDemo", code: 1,
                                     userInfo: [NSLocalizedDescriptionKey: "trail is closed"])
            return ""
        }
        return encode(["trail": name, "max_pitch_deg": 34, "aspect": "NE", "length_m": 1200])
    }
}

/// Collects the stage of every progress event.
final class StageRecorder: NSObject, LoomMobileProgressListenerProtocol {
    private(set) var stages: [String] = []
    private(set) var answer = ""

    func onProgress(_ progressJSON: String?) {
        let event = decode(progressJSON)
        if let stage = event["stage"] as? String, stages.last != stage { stages.append(stage) }
        if event["stage"] as? String == "EXECUTION_STAGE_COMPLETED" {
            answer = event["partial_content"] as? String ?? ""
        }
    }
}

/// Runs three turns against `model` and reports what happened: one streamed,
/// one plain, and one whose tool call fails.
func runDemo(model: LoomMobileModelProviderProtocol, dataDir: URL) -> String {
    let config = encode([
        "data_dir": dataDir.path,
        "agent": [
            "name": "trail-guide",
            "system_prompt": "Answer questions about ski trails. Look up every trail with the trail_lookup tool before answering.",
        ],
        "enable_tracing": true,
    ])

    var error: NSError?
    guard let engine = LoomMobileNewEngine(config, model, &error) else {
        return "LOOM_DEMO_FAILED: \(error?.localizedDescription ?? "engine did not start")"
    }
    defer { try? engine.close() }

    var lines = ["model: \(model.providerName()) / \(model.modelID())"]
    do {
        try engine.register(TrailLookup())

        let recorder = StageRecorder()
        try engine.streamWeave(
            encode(["session_id": "demo", "query": "How steep is Corbet's Couloir?"]), listener: recorder)
        lines.append("stream stages: \(recorder.stages.joined(separator: " > "))")
        lines.append("stream answer: \(recorder.answer)")

        let response = engine.weave(
            encode(["session_id": "demo", "query": "Which way does Rendezvous Bowl face?"]), error: &error)
        if let error { throw error }
        lines.append("weave answer: \(decode(response)["text"] as? String ?? "")")

        let failed = engine.weave(encode(["session_id": "demo", "query": "Is Closed Run open?"]), error: &error)
        if let error { throw error }
        lines.append("failed tool answer: \(decode(failed)["text"] as? String ?? "")")

        let stored = (try? FileManager.default.contentsOfDirectory(atPath: dataDir.path))?.sorted() ?? []
        lines.append("data directory: \(stored.joined(separator: ", "))")
        lines.append("LOOM_DEMO_OK")
    } catch {
        lines.append("LOOM_DEMO_FAILED: \(error.localizedDescription)")
    }
    return lines.joined(separator: "\n")
}

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
import FoundationModels
import LoomMobile

/// Runs model calls on Apple's on-device language model (iOS 26 / macOS 26
/// and later, on hardware that supports Apple Intelligence).
///
/// Loom runs tools itself, while a `LanguageModelSession` expects to run them.
/// To hand control back to Loom, each Loom tool is presented to the session as
/// a tool that records the call and stops the session. Loom then executes the
/// call and asks again with the result in the conversation.
///
/// `chat` blocks the calling thread until the model answers. The engine calls
/// it from its own threads, never from the main thread.
@available(iOS 26.0, macOS 26.0, *)
final class OnDeviceModel: NSObject, LoomMobileModelProviderProtocol {
    /// Asked after tool results, because a session can only generate in
    /// response to a prompt.
    static let continuationPrompt = "Answer the previous request using the tool results above."

    /// Nil when the model can be used, otherwise why it cannot.
    static var unavailableReason: String? {
        switch SystemLanguageModel.default.availability {
        case .available: return nil
        case .unavailable(let reason): return String(describing: reason)
        @unknown default: return "unknown"
        }
    }

    func providerName() -> String { "apple-on-device" }
    func modelID() -> String { "system-language-model" }

    func chat(_ requestJSON: String?, error: NSErrorPointer) -> String {
        let outcome = Outcome()
        let done = DispatchSemaphore(value: 0)
        Task.detached {
            do {
                outcome.set(.success(try await Self.respond(to: decode(requestJSON))))
            } catch {
                outcome.set(.failure(error))
            }
            done.signal()
        }
        done.wait()

        switch outcome.get() {
        case .success(let response):
            return response
        case .failure(let failure):
            error?.pointee = NSError(domain: "OnDeviceModel", code: 1, userInfo: [
                NSLocalizedDescriptionKey: "on-device model: \(String(describing: failure))",
            ])
            return ""
        }
    }

    /// Performs one model call for a decoded `HostModelRequest` and returns a
    /// `HostModelResponse` as JSON.
    private static func respond(to request: [String: Any]) async throws -> String {
        let messages = request["messages"] as? [[String: Any]] ?? []
        let specs = request["tools"] as? [[String: Any]] ?? []

        let captured = CapturedCalls()
        let tools = try specs.map { try BridgedTool(spec: $0, captured: captured) }
        let (entries, prompt) = try transcript(for: messages, tools: tools)

        let session = LanguageModelSession(
            model: SystemLanguageModel.default, tools: tools, transcript: Transcript(entries: entries))
        do {
            let response = try await session.respond(to: prompt)
            return encode(["content": response.content, "stop_reason": "end_turn"])
        } catch {
            // A recorded call means the model chose a tool and the bridged
            // tool stopped the session on purpose.
            let calls = captured.all()
            guard !calls.isEmpty else { throw error }
            return encode(["stop_reason": "tool_use", "tool_calls": calls])
        }
    }

    /// Converts Loom's message list into session history plus the prompt to
    /// respond to.
    private static func transcript(
        for messages: [[String: Any]], tools: [BridgedTool]
    ) throws -> ([Transcript.Entry], String) {
        var remaining = messages[...]

        var instructions: [String] = []
        while let first = remaining.first, first["role"] as? String == "system" {
            instructions.append(first["content"] as? String ?? "")
            remaining = remaining.dropFirst()
        }

        // When the conversation ends with the user's message, that message is
        // the prompt. Otherwise it ends with tool results.
        var prompt = continuationPrompt
        if let last = remaining.last, last["role"] as? String == "user" {
            prompt = last["content"] as? String ?? ""
            remaining = remaining.dropLast()
        }

        var entries: [Transcript.Entry] = [
            .instructions(Transcript.Instructions(
                segments: [text(instructions.joined(separator: "\n\n"))],
                toolDefinitions: tools.map { Transcript.ToolDefinition(tool: $0) })),
        ]
        var toolNames: [String: String] = [:]
        for message in remaining {
            let content = message["content"] as? String ?? ""
            switch message["role"] as? String {
            case "user":
                entries.append(.prompt(Transcript.Prompt(segments: [text(content)])))
            case "system":
                entries.append(.prompt(Transcript.Prompt(segments: [text("System note: \(content)")])))
            case "assistant":
                if !content.isEmpty {
                    entries.append(.response(Transcript.Response(assetIDs: [], segments: [text(content)])))
                }
                let calls = message["tool_calls"] as? [[String: Any]] ?? []
                if !calls.isEmpty {
                    entries.append(.toolCalls(Transcript.ToolCalls(try calls.map { call in
                        let id = call["id"] as? String ?? ""
                        let name = call["name"] as? String ?? ""
                        toolNames[id] = name
                        return Transcript.ToolCall(
                            id: id, toolName: name,
                            arguments: try GeneratedContent(json: encode(call["input"] ?? [String: Any]())))
                    })))
                }
            case "tool":
                let id = message["tool_use_id"] as? String ?? ""
                let failure = message["tool_error"] as? String ?? ""
                let output = failure.isEmpty
                    ? encode(["result": message["tool_result"] ?? NSNull()])
                    : "The tool failed: \(failure)"
                entries.append(.toolOutput(Transcript.ToolOutput(
                    id: id, toolName: toolNames[id] ?? "", segments: [text(output)])))
            default:
                throw BridgeError("unsupported message role \(message["role"] ?? "nil")")
            }
        }
        return (entries, prompt)
    }

    private static func text(_ content: String) -> Transcript.Segment {
        .text(Transcript.TextSegment(content: content))
    }
}

/// A Loom tool as the session sees it. Calling it records the call and stops
/// the session so Loom can execute the tool.
@available(iOS 26.0, macOS 26.0, *)
private struct BridgedTool: Tool {
    let name: String
    let description: String
    let parameters: GenerationSchema
    let captured: CapturedCalls

    init(spec: [String: Any], captured: CapturedCalls) throws {
        name = spec["name"] as? String ?? ""
        description = spec["description"] as? String ?? ""
        let schema = spec["input_schema"] as? [String: Any] ?? ["type": "object"]
        parameters = try GenerationSchema(
            root: try SchemaConverter.convert(schema, name: name), dependencies: [])
        self.captured = captured
    }

    func call(arguments: GeneratedContent) async throws -> String {
        captured.add(name: name, argumentsJSON: arguments.jsonString)
        throw HandBackToLoom()
    }
}

/// Thrown by a bridged tool to stop the session.
private struct HandBackToLoom: Error {}

private struct BridgeError: Error, CustomStringConvertible {
    let description: String
    init(_ description: String) { self.description = description }
}

/// Converts the JSON Schema of a Loom tool into a generation schema. It
/// covers objects, arrays, strings (including enums), numbers, integers and
/// booleans, and rejects anything else by name instead of guessing.
@available(iOS 26.0, macOS 26.0, *)
private enum SchemaConverter {
    static func convert(_ schema: [String: Any], name: String) throws -> DynamicGenerationSchema {
        let description = schema["description"] as? String
        switch schema["type"] as? String {
        case "object":
            let properties = schema["properties"] as? [String: [String: Any]] ?? [:]
            let required = Set(schema["required"] as? [String] ?? [])
            return DynamicGenerationSchema(
                name: name, description: description,
                properties: try properties.keys.sorted().map { key in
                    DynamicGenerationSchema.Property(
                        name: key,
                        description: properties[key]?["description"] as? String,
                        schema: try convert(properties[key] ?? [:], name: "\(name)_\(key)"),
                        isOptional: !required.contains(key))
                })
        case "array":
            let items = schema["items"] as? [String: Any] ?? ["type": "string"]
            return DynamicGenerationSchema(arrayOf: try convert(items, name: "\(name)_item"))
        case "string":
            if let choices = schema["enum"] as? [String], !choices.isEmpty {
                return DynamicGenerationSchema(name: name, description: description, anyOf: choices)
            }
            return DynamicGenerationSchema(type: String.self)
        case "integer":
            return DynamicGenerationSchema(type: Int.self)
        case "number":
            return DynamicGenerationSchema(type: Double.self)
        case "boolean":
            return DynamicGenerationSchema(type: Bool.self)
        default:
            throw BridgeError("tool schema \(name): unsupported JSON Schema \(encode(schema))")
        }
    }
}

/// Tool calls recorded during one model call.
private final class CapturedCalls: @unchecked Sendable {
    private let lock = NSLock()
    private var calls: [[String: Any]] = []

    func add(name: String, argumentsJSON: String) {
        lock.lock()
        defer { lock.unlock() }
        calls.append(["id": UUID().uuidString, "name": name, "input": decode(argumentsJSON)])
    }

    func all() -> [[String: Any]] {
        lock.lock()
        defer { lock.unlock() }
        return calls
    }
}

/// The result of one model call, handed from the async task to the blocked
/// caller.
private final class Outcome: @unchecked Sendable {
    private let lock = NSLock()
    private var result: Result<String, Error> = .failure(BridgeError("no result"))

    func set(_ value: Result<String, Error>) {
        lock.lock()
        defer { lock.unlock() }
        result = value
    }

    func get() -> Result<String, Error> {
        lock.lock()
        defer { lock.unlock() }
        return result
    }
}

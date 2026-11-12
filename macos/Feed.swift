import Foundation
import SwiftUI

struct MarketGroup: Decodable, Identifiable {
    let id: String
    let name: String
    let custom: Bool
}

struct MarketQuote: Decodable, Identifiable {
    let symbol, name, ticker, session, badge, time, price, currency, move: String
    let percent: Double?
    let healthy: Bool
    let source, notice: String
    var id: String { symbol }
}

struct PricePoint: Decodable, Identifiable {
    let time: Int64
    let price: Double
    var id: Int64 { time }
    enum CodingKeys: String, CodingKey {
        case time = "Time"
        case price = "Price"
    }
}

struct Metric: Decodable {
    let label, value: String
}

struct Milestone: Decodable {
    let title, when, values: String
    let published: Bool
    let time: Int64
    let marker: String
}

struct Article: Decodable {
    let title, byline, tag, url: String
    var link: URL? {
        guard let target = URL(string: url), target.scheme?.lowercased() == "https",
              let host = target.host, !host.isEmpty, target.user == nil, target.password == nil else { return nil }
        return target
    }
}

struct Detail: Decodable {
    let metrics: [Metric]
    let milestones: [Milestone]
    let news: [Article]
    let metricNote, macroStatus, newsStatus, closeDate: String
    let closeChange: Double?
}

struct Intraday: Decodable {
    let symbol: String
    let points: [PricePoint]
    let offset: Int
    let base: Double
    let digits: Int
    let loading, sampled: Bool
    let detail: Detail
}

struct Frame: Decodable {
    let language: String
    let interval: Int
    let upIsRed, showNet: Bool
    let status, country, latency, up, down, group: String
    let groups: [MarketGroup]
    let quotes: [MarketQuote]
    let chart: Intraday?
    let notice: String
    let validating: Bool

    func tr(_ chinese: String, _ english: String) -> String {
        language == "en" ? english : chinese
    }
}

func percentText(_ value: Double?) -> String {
    guard let value = value, value.isFinite else { return "–" }
    return String(format: "%+.2f%%", abs(value) < 0.005 ? 0 : value)
}

@MainActor
final class Feed: ObservableObject {
    @Published private(set) var frame: Frame?
    @Published private(set) var selected: String?
    @Published private(set) var failed = false
    var onFrame: ((Frame) -> Void)?

    private var process: Process?
    private var output: FileHandle?
    private var input: FileHandle?
    private var buffer = Data()
    private var generation = UUID()
    private var isVisible = false
    private let writer = DispatchQueue(label: "Samuel.command")

    func start() {
        guard process == nil else { return }
        failed = false
        buffer = Data()
        generation = UUID()
        let id = generation
        let helper = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/SamuelData")
        guard FileManager.default.isExecutableFile(atPath: helper.path) else {
            failed = true
            return
        }
        let child = Process()
        let incoming = Pipe()
        let outgoing = Pipe()
        child.executableURL = helper
        child.standardInput = incoming
        child.standardOutput = outgoing
        child.standardError = FileHandle.nullDevice
        child.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async { self?.exited(id) }
        }
        let read = outgoing.fileHandleForReading
        read.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            if data.isEmpty { handle.readabilityHandler = nil }
            DispatchQueue.main.async { self?.receive(data, id) }
        }
        do {
            try child.run()
            process = child
            input = incoming.fileHandleForWriting
            output = read
        } catch {
            read.readabilityHandler = nil
            failed = true
        }
    }

    private func receive(_ bytes: Data, _ id: UUID) {
        guard id == generation, !bytes.isEmpty else { return }
        buffer.append(bytes)
        while let end = buffer.firstIndex(of: 10) {
            let line = Data(buffer[..<end])
            buffer.removeSubrange(...end)
            guard let latest = try? JSONDecoder().decode(Frame.self, from: line) else { continue }
            let changedGroup = frame?.group != latest.group
            frame = latest
            if changedGroup || !latest.quotes.contains(where: { $0.id == selected }) {
                selected = nil
            }
            if isVisible && selected == nil, let quote = latest.quotes.first {
                select(quote.id)
            }
            onFrame?(latest)
        }
        if buffer.count > 1_048_576 { buffer = Data() }
    }

    private func exited(_ id: UUID) {
        guard id == generation else { return }
        stop()
        selected = nil
        failed = true
    }

    func stop() {
        generation = UUID()
        output?.readabilityHandler = nil
        output = nil
        if let handle = input { writer.async { try? handle.close() } }
        input = nil
        // Closing stdin lets the helper exit; termination also covers a
        // blocked writer or unexpected helper failure.
        if let child = process, child.isRunning { child.terminate() }
        process = nil
    }

    func send(_ action: String, value: String = "", second: String = "", number: Int = 0, on: Bool = false) {
        guard let handle = input,
              var bytes = try? JSONSerialization.data(withJSONObject: [
                "action": action, "value": value, "second": second, "number": number, "on": on
              ]) else { return }
        bytes.append(10)
        let command = bytes
        writer.async { try? handle.write(contentsOf: command) }
    }

    func select(_ symbol: String) {
        selected = symbol
        send("open", value: symbol)
    }

    func setVisible(_ visible: Bool) {
        isVisible = visible
        if visible {
            if let symbol = selected ?? frame?.quotes.first?.id { select(symbol) }
        } else {
            send("close")
        }
    }
}

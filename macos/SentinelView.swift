import Foundation
import SwiftUI

struct SentinelReport: Decodable {
    struct Headline: Decodable {
        let title, source, publishedAt: String
        let url: String?
        var link: URL? {
            guard let url, let target = URL(string: url), target.scheme?.lowercased() == "https",
                  let host = target.host, !host.isEmpty, target.user == nil, target.password == nil else { return nil }
            return target
        }
    }
    struct FearReading: Decodable {
        let level, percentile1Y: Double?
        let date, publishedAt, rating, ratingZh, source: String?
    }
    struct FearIndices: Decodable {
        let vix, cnnFearGreed: FearReading
    }
    struct Dimension: Decodable { let score: Double?; let summary: String }
    struct News: Decodable { let adjustment: Double; let fedTone, pricedInAssessment: String }
    struct Breakdown: Decodable {
        let panicAndBreadth, fedAndLiquidity, systemicCreditRisk: Dimension
        let newsAndCatalysts: News
    }
    let asOfDate, strategyStyle, confidence, analysisMode: String
    let marketTemperature: Int
    let spxCloseDate, ndxCloseDate: String?
    let temperatureDeltaVsPrev: Int?
    let coverageRatio, suggestedDcaMultiplier: Double
    let actionSignal, indexPreference, indexPreferenceRationale, executiveSummary: String
    let actionLabelZh: String?
    let dimensionBreakdown: Breakdown
    let fearIndices: FearIndices?
    let headlines48H: [Headline]?
    let riskDataThrottleApplied, creditDataMissing: Bool?
    let riskDataWarning: String?
    let keyDrivers, guardrailLog, upcomingEventRisks, invalidationTriggers, missingDataWarnings: [String]

    static func decode(_ data: Data) throws -> Self {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(Self.self, from: data)
    }
}

enum SentinelHelper {
    // Both pipes drain independently of the main actor and of one another.
    static func output(_ args: [String], input: Data? = nil, onProcess: (@Sendable (Process?) -> Void)? = nil) async throws -> Data {
        try await Task.detached(priority: .userInitiated) {
            let process = Process()
            let output = Pipe(), diagnostics = Pipe()
            process.executableURL = SamuelHelper.url
            process.arguments = ["sentinel"] + args
            let incoming = input == nil ? nil : Pipe()
            process.standardInput = incoming?.fileHandleForReading ?? FileHandle.nullDevice
            process.standardOutput = output
            process.standardError = diagnostics
            try process.run()
            onProcess?(process)
            defer { onProcess?(nil) }
            let errors = Task.detached { diagnostics.fileHandleForReading.readDataToEndOfFile() }
            if let incoming, let input {
                do { try incoming.fileHandleForWriting.write(contentsOf: input) }
                catch { try? incoming.fileHandleForWriting.close(); throw error }
                try incoming.fileHandleForWriting.close()
            }
            let data = output.fileHandleForReading.readDataToEndOfFile()
            let log = await errors.value
            process.waitUntilExit()
            if process.terminationStatus != 0 {
                let message = String(decoding: log, as: UTF8.self)
                throw NSError(domain: "SamuelData", code: Int(process.terminationStatus),
                              userInfo: [NSLocalizedDescriptionKey: message.isEmpty ? "SamuelData exited (\(process.terminationStatus))" : message])
            }
            return data
        }.value
    }
}

@MainActor
final class SentinelStore: ObservableObject {
    @Published var style = "left_dca"
    @Published var force = false
    @Published private(set) var busy = false
    @Published private(set) var report: SentinelReport?
    @Published private(set) var calibration = ""
    @Published private(set) var failure = ""

    private var loadedStyle: String?
    private var runningProcess: Process?

    func loadLatest() {
        guard !busy, loadedStyle != style else { return }
        let target = style
        Task {
            guard let data = try? await SentinelHelper.output(["latest", "--style", target, "--json"]) else { return }
            guard target == style, !busy else { return }
            loadedStyle = target
            let trimmed = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.isEmpty {
                if report?.strategyStyle != target { report = nil }
                return
            }
            if let decoded = try? SentinelReport.decode(data) {
                report = decoded
                calibration = ""
            }
        }
    }

    func start(calibrate: Bool, refresh: Bool = false) {
        guard !busy else { return }
        busy = true
        failure = ""
        let target = style
        var args = calibrate ? ["calibrate"] : ["run", "--style", target, "--json"]
        if force || refresh { args.append("--force-refresh") }
        Task { [weak self] in
            guard let self else { return }
            defer { self.busy = false }
            do {
                let data = try await SentinelHelper.output(args) { [weak self] proc in
                    DispatchQueue.main.async { self?.runningProcess = proc }
                }
                if calibrate {
                    self.calibration = String(decoding: data, as: UTF8.self)
                    self.report = nil
                    self.loadedStyle = nil
                } else {
                    self.report = try SentinelReport.decode(data)
                    self.calibration = ""
                    self.loadedStyle = target
                }
            } catch { self.failure = error.localizedDescription }
        }
    }

    func stop() {
        if let proc = runningProcess, proc.isRunning { proc.terminate() }
        runningProcess = nil
    }
}

@MainActor
struct SentinelView: View {
    @ObservedObject var store: SentinelStore
    let english: Bool
    let settingsRevision: Int
    let openSettings: () -> Void
    let back: () -> Void

    private func tr(_ zh: String, _ en: String) -> String { english ? en : zh }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Button(action: back) { Image(systemName: "chevron.backward") }
                    .accessibilityLabel(tr("返回行情", "Back to quotes"))
                Text(tr("美股宏观情绪与择时", "US Market Macro Sentinel")).font(.headline)
                Spacer()
                Button(action: openSettings) { Image(systemName: "gearshape") }
                    .help(tr("数据与模型设置", "Data and model settings")).disabled(store.busy)
            }
            Picker(tr("策略", "Strategy"), selection: $store.style) {
                Text(tr("左侧分批定投", "Contrarian DCA")).tag("left_dca")
                Text(tr("右侧反转确认", "Reversal confirmation")).tag("right_confirm")
            }
            .pickerStyle(.segmented).disabled(store.busy)
            HStack {
                Button(tr("运行今日研判", "Run analysis")) { store.start(calibrate: false) }
                    .buttonStyle(.borderedProminent)
                Button(tr("回填与胜率", "Calibrate")) { store.start(calibrate: true) }
                Toggle(tr("强制刷新", "Force refresh"), isOn: $store.force).toggleStyle(.checkbox)
                if store.busy { ProgressView().controlSize(.small) }
            }
            .disabled(store.busy)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    if store.busy && store.report != nil {
                        Text(tr("正在更新行情与消息面研判，以下先显示最近一次结果…", "Refreshing market data and analysis; showing the latest saved report…"))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    if !store.failure.isEmpty { Text(store.failure).foregroundStyle(.red) }
                    if !store.calibration.isEmpty { Text(store.calibration).monospacedDigit() }
                    if let r = store.report { details(r) }
                    if store.report == nil && store.calibration.isEmpty && store.failure.isEmpty {
                        Text(store.busy ? tr("正在读取行情与宏观数据…", "Reading market and economic data…")
                                        : tr("选择策略后运行，查看风险温度、定投倍率及其依据。", "Run a strategy to see the risk temperature, DCA multiplier and supporting data."))
                            .foregroundStyle(.secondary)
                    }
                }.frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
            }
        }
        .padding(16)
        .onAppear { store.loadLatest() }
        .onChange(of: store.style) { _ in store.loadLatest() }
        .onChange(of: settingsRevision) { _ in
            if store.report != nil && !store.busy { store.start(calibrate: false, refresh: true) }
        }
    }

    @ViewBuilder private func details(_ r: SentinelReport) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text("\(r.marketTemperature)/100").font(.largeTitle.bold()).monospacedDigit()
            if let delta = r.temperatureDeltaVsPrev { Text(String(format: "%+d", delta)).foregroundStyle(.secondary) }
            Spacer()
            Text(String(format: "%.2f×", r.suggestedDcaMultiplier)).font(.title2.bold())
            Text(r.indexPreference).font(.headline)
        }
        ProgressView(value: Double(r.marketTemperature), total: 100)
            .accessibilityLabel(tr("风险温度", "Risk temperature"))
        if r.riskDataThrottleApplied == true, let warning = r.riskDataWarning {
            Label(tr(warning, r.creditDataMissing == true
                     ? "High-yield credit spreads are missing. Recession risk is unknown. Extra DCA is paused."
                     : "Indicator coverage is below 60%. Extra DCA is paused."), systemImage: "exclamationmark.triangle.fill")
                .font(.callout.weight(.semibold)).foregroundStyle(.orange)
        }
        if let indices = r.fearIndices { fearReadings(indices) }
        HStack {
            Text(english ? r.actionSignal.replacingOccurrences(of: "_", with: " ") : (r.actionLabelZh ?? r.actionSignal))
                .font(.callout.bold()).padding(.vertical, 3).padding(.horizontal, 8)
                .background(Color.accentColor.opacity(0.15), in: Capsule())
            Text(r.asOfDate).font(.callout)
            Text(r.strategyStyle == "right_confirm" ? tr("右侧确认", "Reversal") : tr("左侧定投", "Contrarian"))
                .font(.caption).foregroundStyle(.secondary)
        }
        let summaryText: String = {
            guard let warning = r.riskDataWarning else { return r.executiveSummary }
            return r.executiveSummary
                .replacingOccurrences(of: warning + "。", with: "")
                .replacingOccurrences(of: warning + "；", with: "")
                .trimmingCharacters(in: .whitespacesAndNewlines)
        }()
        Text(summaryText)
        Text(r.indexPreferenceRationale)
        if r.spxCloseDate != nil || r.ndxCloseDate != nil {
            Text("\(tr("收盘日期", "Closing dates")) · SPX \(r.spxCloseDate ?? "—") · NDX \(r.ndxCloseDate ?? "—")")
                .font(.caption).foregroundStyle(.secondary)
        }
        Text("\(r.confidence) · \(String(format: "%.0f%%", r.coverageRatio * 100)) \(tr("指标覆盖", "coverage")) · \(r.analysisMode)")
            .font(.caption).foregroundStyle(.secondary)
        Divider()
        dimension(tr("恐慌与广度", "Panic and breadth"), r.dimensionBreakdown.panicAndBreadth)
        dimension(tr("美联储与流动性", "Fed and liquidity"), r.dimensionBreakdown.fedAndLiquidity)
        if r.creditDataMissing == true {
            VStack(alignment: .leading, spacing: 3) {
                let suffix = r.dimensionBreakdown.systemicCreditRisk.score.map {
                    String(format: "  %.1f", $0) + tr("（仅含趋势项，缺信用利差）", " (trend only, credit spread missing)")
                } ?? tr("  —（缺信用数据）", "  — (credit data unavailable)")
                Text(tr("系统性信用", "Systemic credit") + suffix).font(.headline)
                Text(r.dimensionBreakdown.systemicCreditRisk.summary)
            }
        } else {
            dimension(tr("系统性信用", "Systemic credit"), r.dimensionBreakdown.systemicCreditRisk)
        }
        let news = r.dimensionBreakdown.newsAndCatalysts
        let tone = r.analysisMode == "quant_only" ? tr("鹰鸽立场未评估", "Fed tone not assessed") : news.fedTone
        Text("\(tr("消息与催化", "News and catalysts"))  \(String(format: "%+.1f", news.adjustment)) · \(tone)").font(.headline)
        Text(news.pricedInAssessment)
        if let headlines = r.headlines48H {
            DisclosureGroup(tr("近 48 小时宏观与美联储快讯", "Macro and Fed headlines · past 48h") + " (\(headlines.count))") {
                VStack(alignment: .leading, spacing: 6) {
                    if headlines.isEmpty { Text(tr("未取得此时段的快讯", "No headlines available for this period")).foregroundStyle(.secondary) }
                    ForEach(headlines.indices, id: \.self) { index in
                        SentinelHeadlineRow(headline: headlines[index], timestamp: Self.marketTime(headlines[index].publishedAt))
                    }
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        section(tr("风控覆写", "Guardrails"), r.guardrailLog)
        section(tr("事件风险", "Event risks"), r.upcomingEventRisks)
        section(tr("关键事实", "Key drivers"), r.keyDrivers)
        section(tr("失效条件", "Invalidation triggers"), r.invalidationTriggers)
        section(tr("缺失数据", "Missing data"), r.missingDataWarnings)
        if !r.missingDataWarnings.isEmpty {
            HStack {
                Button(tr("数据与模型设置", "Data and model settings"), action: openSettings)
                Button(tr("重新获取数据", "Refresh sources")) { store.start(calibrate: false, refresh: true) }
            }.disabled(store.busy)
        }
    }

    private func fearReadings(_ indices: SentinelReport.FearIndices) -> some View {
        let vix = indices.vix, cnn = indices.cnnFearGreed
        var vixInfo: [String] = [], cnnInfo: [String] = []
        if let date = vix.date { vixInfo.append(tr("收盘 ", "Close ") + date) }
        if let rank = vix.percentile1Y {
            vixInfo.append(tr("一年分位 ", "1Y percentile ") + String(format: "%.0f%%", rank))
        }
        if let raw = cnn.publishedAt { cnnInfo.append(Self.marketTime(raw)) }
        if cnn.source == "CNN Business (public archive)" { cnnInfo.append(tr("公开存档", "Public archive")) }
        if let rating = cnn.rating {
            cnnInfo.insert(tr(cnn.ratingZh ?? rating, rating.capitalized), at: 0)
        }
        return VStack(alignment: .leading, spacing: 6) {
            fearRow(tr("VIX 恐慌指数", "Cboe VIX"), vix.level, tr(" 点", " pts"), vixInfo)
            fearRow(tr("CNN 恐惧与贪婪指数", "CNN Fear & Greed"), cnn.level, "/100", cnnInfo)
        }
    }

    private func fearRow(_ name: String, _ value: Double?, _ unit: String, _ info: [String]) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .firstTextBaseline) {
                Text(name).font(.callout)
                Spacer()
                if let value {
                    Text(String(format: "%.2f", value) + unit).font(.callout.bold()).monospacedDigit()
                } else {
                    Text(tr("暂无数据", "Unavailable")).font(.callout).foregroundStyle(.secondary)
                }
            }
            if value != nil && !info.isEmpty {
                Text(info.joined(separator: " · ")).font(.caption).foregroundStyle(.secondary)
            }
        }.accessibilityElement(children: .combine)
    }

    private static func marketTime(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let fractional = iso.date(from: raw)
        iso.formatOptions = [.withInternetDateTime]
        guard let date = fractional ?? iso.date(from: raw) else { return raw }
        let format = DateFormatter()
        format.locale = Locale(identifier: "en_US_POSIX")
        format.timeZone = TimeZone(identifier: "America/New_York")
        format.dateFormat = "yyyy-MM-dd HH:mm z"
        return format.string(from: date)
    }

    private func dimension(_ title: String, _ d: SentinelReport.Dimension) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(d.score.map { title + "  " + String(format: "%.1f", $0) } ?? title).font(.headline)
            Text(d.summary)
        }
    }

    @ViewBuilder private func section(_ title: String, _ entries: [String]) -> some View {
        if !entries.isEmpty {
            DisclosureGroup(title) {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(entries.indices, id: \.self) { i in Text("• " + entries[i]) }
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

private struct SentinelHeadlineRow: View {
    let headline: SentinelReport.Headline
    let timestamp: String
    @ViewState private var hover = false

    var body: some View {
        Group {
            if let link = headline.link {
                Link(destination: link) { content(clickable: true) }
                    .buttonStyle(.plain)
                    .help(headline.url ?? headline.title)
            } else {
                content(clickable: false)
            }
        }
        .onHover { hover = $0 }
    }

    private func content(clickable: Bool) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(headline.title)
                    .foregroundStyle(clickable && hover ? Color.accentColor : Color.primary)
                if clickable {
                    Image(systemName: "arrow.up.right")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            Text(headline.source + " · " + timestamp).font(.caption).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.vertical, 4)
        .padding(.horizontal, 6)
        .background(clickable && hover ? Color.accentColor.opacity(0.12) : Color.clear,
                    in: RoundedRectangle(cornerRadius: 6))
        .contentShape(Rectangle())
    }
}

import SwiftUI

@MainActor
struct Panel: View {
    @ObservedObject var feed: Feed
    let size: CGSize
    let close: () -> Void
    let quit: () -> Void
    @State private var adding = false
    @State private var first = ""
    @State private var second = ""

    var body: some View {
        Group {
            if let state = feed.frame, !feed.failed {
                content(state)
            } else {
                VStack(spacing: 14) {
                    Image(systemName: "chart.line.uptrend.xyaxis").font(.system(size: 32))
                    Text(feed.failed ? "行情服务未启动 / Data service unavailable" : "正在连接行情 / Connecting")
                        .foregroundStyle(.secondary)
                    if feed.failed { Button("重试 / Retry") { feed.start() } }
                    Button("退出 Samuel / Quit Samuel", action: quit)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(width: size.width, height: size.height)
        .onExitCommand(perform: close)
    }

    private func content(_ state: Frame) -> some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Image(systemName: "chart.line.uptrend.xyaxis")
                    .foregroundStyle(.tint)
                Picker(state.tr("行情分组", "Market group"), selection: Binding(
                    get: { state.group }, set: { feed.send("group", value: $0) }
                )) {
                    ForEach(state.groups) { group in
                        Text(group.name).tag(group.id)
                    }
                }
                .labelsHidden()
                .accessibilityLabel(state.tr("行情分组", "Market group"))
                Button { feed.send("refresh") } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .help(state.tr("刷新行情", "Refresh quotes"))
                settings(state)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            if adding {
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        TextField(state.tr("代码一，如 NVDA", "First symbol, e.g. NVDA"), text: $first)
                        TextField(state.tr("代码二，如 600519", "Second symbol, e.g. 600519"), text: $second)
                        Button(state.tr("验证并添加", "Verify & add")) {
                            feed.send("add", value: first, second: second)
                        }
                        .disabled(state.validating || first.isEmpty || second.isEmpty)
                        Button { adding = false } label: { Image(systemName: "xmark") }
                    }
                    if state.validating {
                        Text(state.tr("正在验证标的…", "Verifying symbols…")).font(.caption)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 10)
                .onChange(of: state.group) { _ in adding = false }
            }
            if !state.notice.isEmpty {
                Text(state.notice).font(.caption).foregroundStyle(.orange)
                    .padding(.horizontal, 16).padding(.bottom, 6)
            }
            Divider()
            if state.quotes.isEmpty {
                Text(state.tr("等待市值排名", "Waiting for market-cap ranking"))
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                HStack(spacing: 8) {
                    ForEach(state.quotes) { quote in quoteButton(quote, state) }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                if let quote = state.quotes.first(where: { $0.id == feed.selected }) {
                    ScrollView {
                        details(quote, state).id(quote.id)
                            .padding(.horizontal, 16).padding(.bottom, 16)
                    }
                    .id(quote.id)
                } else {
                    Spacer(minLength: 0)
                }
            }
        }
    }

    private func settings(_ state: Frame) -> some View {
        Menu {
            Menu(state.tr("界面语言", "Language")) {
                Button("简体中文") { feed.send("language", value: "zh") }
                Button("English") { feed.send("language", value: "en") }
            }
            Menu(state.tr("刷新频率", "Refresh interval")) {
                ForEach([1, 2, 5], id: \.self) { seconds in
                    Button("\(seconds)s" + (seconds == state.interval ? " ✓" : "")) {
                        feed.send("interval", number: seconds)
                    }
                }
            }
            Toggle(state.tr("菜单栏显示网速", "Show network rates"), isOn: Binding(
                get: { state.showNet }, set: { feed.send("network", on: $0) }
            ))
            Toggle(state.tr("红涨绿跌", "Red for gains"), isOn: Binding(
                get: { state.upIsRed }, set: { feed.send("color", on: $0) }
            ))
            Divider()
            Button(state.tr("添加自定义组合…", "Add custom pair…")) { adding = true }
            if state.groups.first(where: { $0.id == state.group })?.custom == true {
                Button(state.tr("删除当前自定义组合", "Remove current custom pair")) {
                    feed.send("remove", value: state.group)
                }
            }
            Divider()
            Button(state.tr("退出 Samuel", "Quit Samuel"), action: quit)
        } label: {
            Image(systemName: "gearshape")
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .accessibilityLabel(state.tr("设置", "Settings"))
    }

    private func quoteButton(_ quote: MarketQuote, _ state: Frame) -> some View {
        let isSelected = quote.id == feed.selected
        return Button {
            feed.select(quote.id)
        } label: {
            HStack(spacing: 8) {
                Text(quote.badge).font(.caption)
                Text(quote.name).lineLimit(1)
                Spacer(minLength: 0)
                Text(percentText(quote.percent)).monospacedDigit()
                    .foregroundStyle(changeTint(quote.percent, state.upIsRed))
            }
            .font(.callout.weight(.semibold))
            .padding(8)
            .background(isSelected ? Color.accentColor.opacity(0.18) : Color.secondary.opacity(0.08),
                        in: RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }

    private func details(_ quote: MarketQuote, _ state: Frame) -> some View {
        let tint = changeTint(quote.percent, state.upIsRed)
        return VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(quote.name).font(.headline).lineLimit(1)
                if quote.name != quote.ticker {
                    Text(quote.ticker).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Text(quote.session).font(.caption.weight(.semibold)).foregroundStyle(.tint)
            }
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(quote.price).font(.system(size: 40, weight: .bold)).monospacedDigit()
                    .minimumScaleFactor(0.6).lineLimit(1)
                Text(quote.currency).font(.callout).foregroundStyle(.secondary)
                Spacer(minLength: 2)
                Text(quote.move + "  " + percentText(quote.percent))
                    .font(.title3.bold()).monospacedDigit().foregroundStyle(tint)
                    .minimumScaleFactor(0.7).lineLimit(1)
            }
            VStack(alignment: .leading, spacing: 3) {
                Text(quote.time)
                if !quote.source.isEmpty { Text(quote.source) }
                if !quote.healthy && !quote.notice.isEmpty {
                    Text(quote.notice).foregroundStyle(.orange)
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            if let chart = state.chart, chart.symbol == quote.id {
                IntradayPlot(chart: chart, frame: state, tint: tint).id(chart.symbol)
                detailSections(chart.detail, state)
            } else {
                Text(state.tr("正在打开分时卡片…", "Opening chart…"))
                    .font(.callout).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 70)
            }
        }
    }

    private func detailSections(_ detail: Detail, _ state: Frame) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(state.tr("上次常规盘收盘", "Last regular close"))
                if !detail.closeDate.isEmpty { Text(detail.closeDate).foregroundStyle(.secondary) }
                Spacer()
                Text(percentText(detail.closeChange)).fontWeight(.semibold).monospacedDigit()
                    .foregroundStyle(changeTint(detail.closeChange, state.upIsRed))
            }
            .font(.callout)
            Divider().padding(.vertical, 3)
            Text(state.tr("盘口与基本面", "Market data & fundamentals")).font(.headline)
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 6), count: 3), spacing: 6) {
                ForEach(Array(detail.metrics.enumerated()), id: \.offset) { _, metric in
                    VStack(alignment: .leading, spacing: 3) {
                        Text(metric.label).font(.caption).foregroundStyle(.secondary)
                        Text(metric.value).font(.callout.weight(.medium)).monospacedDigit()
                            .lineLimit(2).minimumScaleFactor(0.85)
                    }
                    .frame(maxWidth: .infinity, minHeight: 42, alignment: .leading)
                    .padding(.horizontal, 8)
                    .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                }
            }
            if !detail.metricNote.isEmpty { note(detail.metricNote) }
            Divider().padding(.vertical, 3)
            Text(state.tr("关键宏观与议息节点", "Macro & rate decisions")).font(.headline)
            if detail.milestones.isEmpty { note(detail.macroStatus) }
            ForEach(Array(detail.milestones.enumerated()), id: \.offset) { _, event in
                VStack(alignment: .leading, spacing: 3) {
                    Text(event.title).font(.callout.weight(.medium))
                    Text(event.when).font(.caption).foregroundStyle(.secondary)
                    if !event.values.isEmpty {
                        Text(event.values).font(.caption)
                            .foregroundStyle(event.published ? Color.teal : Color.secondary)
                    }
                }
                .padding(.vertical, 2)
            }
            Divider().padding(.vertical, 3)
            Text(state.tr("财报与关联资讯", "Earnings & related news")).font(.headline)
            if detail.news.isEmpty { note(detail.newsStatus) }
            ForEach(Array(detail.news.enumerated()), id: \.offset) { _, article in
                NewsRow(article: article)
            }
        }
    }

    private func note(_ text: String) -> some View {
        Text(text).font(.caption).foregroundStyle(.secondary)
    }
}

private struct NewsRow: View {
    let article: Article
    @State private var hover = false

    var body: some View {
        Group {
            if let link = article.link {
                Link(destination: link) { caption }.buttonStyle(.plain)
                    .help(article.title)
            } else {
                caption
            }
        }
        .onHover { hover = $0 }
    }

    private var caption: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text(article.tag).font(.caption).foregroundStyle(.tint)
                Text(article.title).lineLimit(1).font(.callout)
            }
            Text(article.byline).font(.caption).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(6)
        .background(hover ? Color.accentColor.opacity(0.12) : Color.clear, in: RoundedRectangle(cornerRadius: 6))
        .contentShape(Rectangle())
    }
}

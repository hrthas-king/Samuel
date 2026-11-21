import SwiftUI

struct IntradayPlot: View {
    let chart: Intraday
    let frame: Frame
    let tint: Color
    @ViewState private var hovered: Int64?
    @ViewState private var pinned: Int64?

    private var active: PricePoint? {
        if let pinned, let point = chart.points.first(where: { $0.time == pinned }) {
            return point
        }
        return chart.points.first { $0.time == hovered }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            ZStack {
                if chart.points.isEmpty {
                    Text(chart.loading ? frame.tr("正在读取分时数据…", "Loading intraday data…")
                         : frame.tr("此时段暂无可核实的分时数据", "No verified trades in this window"))
                        .font(.callout)
                        .foregroundStyle(.secondary)
                } else {
                    GeometryReader { proxy in
                        Canvas { context, size in draw(context, size) }
                            .contentShape(Rectangle())
                            .onContinuousHover { state in
                                switch state {
                                case .active(let location): hovered = nearest(location.x, proxy.size.width)
                                case .ended: hovered = nil
                                }
                            }
                            .gesture(DragGesture(minimumDistance: 0).onEnded { drag in
                                let stamp = nearest(drag.location.x, proxy.size.width)
                                pinned = pinned == stamp ? nil : stamp
                            })
                    }
                }
            }
            .frame(maxWidth: .infinity, minHeight: 132, maxHeight: 132)
            .accessibilityLabel(frame.tr("分时走势图", "Intraday price chart"))
            HStack {
                Text(chart.window.label(chart.window.start))
                Spacer(minLength: 4)
                Text(frame.tr("北京时间", "Beijing time") + " (UTC+8)")
                Spacer(minLength: 4)
                Text(chart.window.label(chart.window.end))
            }
            .font(.caption2.monospacedDigit())
            .foregroundStyle(.secondary)
            .padding(.horizontal, 8)
            HStack(spacing: 6) {
                if let point = active {
                    Text(chart.window.label(point.time))
                    Text(String(format: "%.*f", chart.digits, point.price))
                    if chart.base.isFinite && chart.base > 0 {
                        let move = (point.price - chart.base) / chart.base * 100
                        Text(percentText(move)).foregroundStyle(changeTint(move, frame.upIsRed))
                    }
                    if pinned == point.time { Image(systemName: "pin.fill").font(.caption2) }
                } else if chart.sampled {
                    Text(frame.tr("含打开卡片后采样", "Includes quotes observed while open"))
                } else if chart.base > 0 {
                    Text(frame.tr("涨跌基准", "Change reference"))
                    Text(String(format: "%.*f", chart.digits, chart.base))
                } else {
                    Text(frame.tr("悬停或点击查看取点", "Hover or click to inspect a point"))
                }
                Spacer(minLength: 0)
            }
            .font(.caption.monospacedDigit())
            .foregroundStyle(.secondary)
            .frame(height: 16)
        }
    }

    private func nearest(_ x: CGFloat, _ width: CGFloat) -> Int64? {
        let span = width - 16
        guard span > 0 else { return nil }
        return chart.window.nearest(chart.points, breaks: chart.breaks,
                                    fraction: Double((x - 8) / span), tolerance: Double(3 / span))
    }

    private func draw(_ ctx: GraphicsContext, _ size: CGSize) {
        let prices = chart.points.map(\.price)
        guard let low = prices.min(), let high = prices.max(), size.width > 16, size.height > 26 else { return }
        let top: CGFloat = 18
        let bottom = size.height - 8
        let minimum = max(high * 0.0002, pow(10, -Double(chart.digits)))
        let range = max(high - low, minimum)
        let lower = (high + low - range) / 2 - 0.1 * range
        let upper = lower + range * 1.2
        func x(_ time: Int64) -> CGFloat {
            8 + (size.width - 16) * CGFloat(chart.window.fraction(time))
        }
        func y(_ price: Double) -> CGFloat {
            bottom - (bottom - top) * CGFloat((price - lower) / (upper - lower))
        }
        for fraction in [CGFloat(0.22), 0.52, 0.82] {
            let at = top + (bottom - top) * fraction
            ctx.stroke(segment(CGPoint(x: 8, y: at), CGPoint(x: size.width - 8, y: at)),
                       with: .color(.secondary.opacity(0.17)), style: StrokeStyle(lineWidth: 1, dash: [3, 4]))
        }
        if chart.base >= lower && chart.base <= upper {
            let at = y(chart.base)
            ctx.stroke(segment(CGPoint(x: 8, y: at), CGPoint(x: size.width - 8, y: at)),
                       with: .color(.secondary.opacity(0.55)), style: StrokeStyle(lineWidth: 1, dash: [4, 4]))
        }
        let points = chart.points.map { CGPoint(x: x($0.time), y: y($0.price)) }
        guard let last = points.last else { return }
        var line = Path(), fill = Path()
        var from = 0
        for to in chart.breaks + [points.count] {
            let run = points[from..<to]
            if run.count == 1 {
                dot(ctx, points[from], radius: 1.6, tint)
            } else {
                var part = Path()
                part.addLines(Array(run))
                line.addPath(part)
                part.addLine(to: CGPoint(x: points[to - 1].x, y: bottom))
                part.addLine(to: CGPoint(x: points[from].x, y: bottom))
                part.closeSubpath()
                fill.addPath(part)
            }
            from = to
        }
        ctx.fill(fill, with: .linearGradient(Gradient(colors: [tint.opacity(0.30), tint.opacity(0)]),
                                            startPoint: CGPoint(x: 0, y: top), endPoint: CGPoint(x: 0, y: bottom)))
        var previousMarker: CGFloat = -100
        for event in chart.detail.milestones.sorted(by: { $0.time < $1.time })
            where !event.marker.isEmpty && event.time >= chart.window.start && event.time <= chart.window.end {
            let at = x(event.time)
            guard at - previousMarker > 34 else { continue }
            previousMarker = at
            ctx.stroke(segment(CGPoint(x: at, y: top), CGPoint(x: at, y: bottom)),
                       with: .color(.secondary.opacity(0.55)), style: StrokeStyle(lineWidth: 1, dash: [2, 3]))
            ctx.draw(Text(event.marker).font(.system(size: 9)).foregroundColor(.secondary),
                     at: CGPoint(x: min(max(at, 24), size.width - 24), y: 8))
        }
        ctx.stroke(line, with: .color(tint), style: StrokeStyle(lineWidth: 2, lineCap: .round, lineJoin: .round))
        dot(ctx, last, radius: 7, tint.opacity(0.18))
        dot(ctx, last, radius: 3, tint)
        if let point = active {
            let position = CGPoint(x: x(point.time), y: y(point.price))
            ctx.stroke(segment(CGPoint(x: position.x, y: top), CGPoint(x: position.x, y: bottom)),
                       with: .color(.secondary), style: StrokeStyle(lineWidth: 1, dash: [3, 3]))
            dot(ctx, position, radius: 4, tint)
            ctx.stroke(Path(ellipseIn: CGRect(x: position.x - 4, y: position.y - 4, width: 8, height: 8)),
                       with: .color(.white.opacity(0.9)), lineWidth: 1)
        }
    }

    private func segment(_ a: CGPoint, _ b: CGPoint) -> Path {
        var path = Path()
        path.move(to: a)
        path.addLine(to: b)
        return path
    }

    private func dot(_ ctx: GraphicsContext, _ p: CGPoint, radius: CGFloat, _ color: Color) {
        ctx.fill(Path(ellipseIn: CGRect(x: p.x - radius, y: p.y - radius, width: 2 * radius, height: 2 * radius)),
                 with: .color(color))
    }
}

func changeTint(_ move: Double?, _ upIsRed: Bool) -> Color {
    guard let move = move, abs(move) >= 0.005 else { return .secondary }
    return (move > 0) == upIsRed ? .red : .green
}

import SwiftUI

struct IntradayPlot: View {
    let chart: Intraday
    let frame: Frame
    let tint: Color
    @State private var hovered: Int64?
    @State private var pinned: Int64?

    private var active: PricePoint? {
        chart.points.first { $0.time == (pinned ?? hovered) }
    }

    var body: some View {
        if let first = chart.points.first, let last = chart.points.last, first.time < last.time {
            VStack(alignment: .leading, spacing: 6) {
                GeometryReader { proxy in
                    let start = Double(first.time)
                    let end = Double(last.time)
                    Canvas { context, size in
                        draw(context, size, start, end)
                    }
                    .contentShape(Rectangle())
                    .onContinuousHover { state in
                        switch state {
                        case .active(let location):
                            hovered = nearest(location.x, proxy.size.width, start, end)
                        case .ended: hovered = nil
                        }
                    }
                    .gesture(DragGesture(minimumDistance: 0).onEnded { drag in
                        let stamp = nearest(drag.location.x, proxy.size.width, start, end)
                        pinned = pinned == stamp ? nil : stamp
                    })
                }
                .frame(height: 132)
                .accessibilityLabel(frame.tr("分时走势图", "Intraday price chart"))
                HStack(spacing: 6) {
                    if let point = active {
                        Text(sampleTime(point.time))
                        Text(String(format: "%.*f", chart.digits, point.price))
                        if chart.base.isFinite && chart.base > 0 {
                            let move = (point.price - chart.base) / chart.base * 100
                            Text(percentText(move)).foregroundStyle(changeTint(move, frame.upIsRed))
                        }
                        if pinned == point.time { Image(systemName: "pin.fill").font(.caption2) }
                    } else if chart.sampled {
                        Text(frame.tr("本次打开期间的行情采样", "Quotes observed while this chart is open"))
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
        } else {
            Text(chart.loading ? frame.tr("正在读取分时数据…", "Loading intraday data…")
                 : frame.tr("暂无可核实的分时数据", "No verified intraday data available"))
                .font(.callout)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, minHeight: 80)
        }
    }

    private func nearest(_ x: CGFloat, _ width: CGFloat, _ start: Double, _ end: Double) -> Int64? {
        guard width > 16 else { return nil }
        let target = start + Double(min(max((x - 8) / (width - 16), 0), 1)) * (end - start)
        return chart.points.min { abs(Double($0.time) - target) < abs(Double($1.time) - target) }?.time
    }

    private func sampleTime(_ seconds: Int64) -> String {
        let format = DateFormatter()
        format.locale = Locale(identifier: "en_US_POSIX")
        format.timeZone = TimeZone(secondsFromGMT: chart.offset)
        format.dateFormat = "MM-dd HH:mm"
        return format.string(from: Date(timeIntervalSince1970: Double(seconds)))
    }

    private func draw(_ ctx: GraphicsContext, _ size: CGSize, _ start: Double, _ end: Double) {
        let prices = chart.points.map(\.price)
        guard let low = prices.min(), let high = prices.max(), size.width > 16, size.height > 26 else { return }
        let top: CGFloat = 18
        let bottom = size.height - 8
        let minimum = max(high * 0.0002, pow(10, -Double(chart.digits)))
        let range = max(high - low, minimum)
        let lower = (high + low - range) / 2 - 0.1 * range
        let upper = lower + range * 1.2
        func x(_ time: Int64) -> CGFloat {
            8 + (size.width - 16) * CGFloat((Double(time) - start) / (end - start))
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
        guard let first = points.first, let last = points.last else { return }
        var line = Path()
        line.addLines(points)
        var fill = line
        fill.addLine(to: CGPoint(x: last.x, y: bottom))
        fill.addLine(to: CGPoint(x: first.x, y: bottom))
        fill.closeSubpath()
        ctx.fill(fill, with: .linearGradient(Gradient(colors: [tint.opacity(0.30), tint.opacity(0)]),
                                            startPoint: CGPoint(x: 0, y: top), endPoint: CGPoint(x: 0, y: bottom)))
        var previousMarker: CGFloat = -100
        for event in chart.detail.milestones.sorted(by: { $0.time < $1.time })
            where !event.marker.isEmpty && Double(event.time) >= start && Double(event.time) <= end {
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

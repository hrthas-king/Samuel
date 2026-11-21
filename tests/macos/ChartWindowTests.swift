import Foundation

@main
enum ChartWindowTests {
    static func main() {
        let start = ISO8601DateFormatter().date(from: "2026-12-07T02:00:00Z")!
        let first = Int64(start.timeIntervalSince1970)
        let window = ChartWindow(start: first, end: first + 23 * 3600)
        precondition(window.label(first) == "12-07 10:00")
        precondition(window.label(window.end) == "12-08 09:00")
        let midnight = first + 14 * 3600
        precondition(window.label(midnight) == "12-08 00:00")
        precondition(abs(window.fraction(midnight) - 14.0 / 23) < 1e-9)

        let minutes: [Int64] = [30, 40, 100]
        let points = minutes.map { PricePoint(time: first + $0 * 60, price: Double($0)) }
        func hit(_ minute: Int) -> Int64? {
            window.nearest(points, breaks: [2], fraction: Double(minute) / 1380, tolerance: 3.0 / 1380)
        }
        precondition(hit(0) == nil && hit(70) == nil && hit(1380) == nil)
        precondition(hit(30) == points[0].time && hit(36) == points[1].time)
        precondition(hit(99) == points[2].time && hit(105) == nil)
        precondition(window.nearest([], breaks: [], fraction: 0.5, tolerance: 0) == nil)
        let close = PricePoint(time: window.end, price: 1)
        precondition(window.nearest([close], breaks: [], fraction: 1, tolerance: 0) == close.time)
    }
}

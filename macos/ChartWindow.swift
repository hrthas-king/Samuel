import Foundation

struct PricePoint: Decodable, Identifiable {
    let time: Int64
    let price: Double
    var id: Int64 { time }
    enum CodingKeys: String, CodingKey {
        case time = "Time"
        case price = "Price"
    }
}

struct ChartWindow: Decodable {
    let start, end: Int64

    private static let clock: DateFormatter = {
        let format = DateFormatter()
        format.locale = Locale(identifier: "en_US_POSIX")
        format.timeZone = TimeZone(secondsFromGMT: 8 * 3600)
        format.dateFormat = "MM-dd HH:mm"
        return format
    }()

    func label(_ stamp: Int64) -> String {
        Self.clock.string(from: Date(timeIntervalSince1970: Double(stamp)))
    }

    func fraction(_ stamp: Int64) -> Double {
        Double(stamp - start) / Double(end - start)
    }

    func nearest(_ points: [PricePoint], breaks: [Int], fraction: Double, tolerance: Double) -> Int64? {
        guard start < end, (0...1).contains(fraction), !points.isEmpty else { return nil }
        let target = Double(start) + fraction * Double(end - start)
        var lo = 0, hi = points.count
        while lo < hi {
            let mid = (lo + hi) / 2
            if Double(points[mid].time) < target { lo = mid + 1 } else { hi = mid }
        }
        let index: Int
        if lo == points.count || (lo > 0 && target - Double(points[lo - 1].time) <= Double(points[lo].time) - target) {
            index = lo - 1
        } else {
            index = lo
        }
        let stamp = points[index].time
        if lo == 0 || lo == points.count || breaks.contains(lo) {
            guard abs(self.fraction(stamp) - fraction) <= tolerance else { return nil }
        }
        return stamp
    }
}

import AppKit
import Darwin
import SwiftUI

@MainActor
final class SamuelDelegate: NSObject, NSApplicationDelegate, NSPopoverDelegate {
    private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let popover = NSPopover()
    private let feed = Feed()

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard let button = item.button else { return }
        button.title = "Samuel …"
        button.toolTip = "Samuel"
        button.setAccessibilityLabel("Samuel")
        button.target = self
        button.action = #selector(togglePopover)
        popover.behavior = .transient
        popover.delegate = self
        feed.onFrame = { [weak self] state in self?.showStatus(state) }
        feed.start()
    }

    @objc private func togglePopover() {
        if popover.isShown {
            popover.performClose(nil)
            return
        }
        guard let button = item.button else { return }
        let work = button.window?.screen?.visibleFrame ?? NSScreen.main?.visibleFrame
        let size = CGSize(width: min(484, (work?.width ?? 540) - 28),
                          height: min(592, (work?.height ?? 655) - 38))
        let panel = Panel(feed: feed, size: size, close: { [weak self] in self?.popover.performClose(nil) },
                          quit: { NSApp.terminate(nil) })
        popover.contentSize = size
        popover.contentViewController = NSHostingController(rootView: panel)
        feed.setVisible(true)
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        popover.contentViewController?.view.window?.makeKey()
    }

    func popoverDidClose(_ notification: Notification) {
        feed.setVisible(false)
        popover.contentViewController = nil
    }

    func applicationWillTerminate(_ notification: Notification) {
        feed.stop()
        NSStatusBar.system.removeStatusItem(item)
    }

    private func showStatus(_ state: Frame) {
        guard let button = item.button else { return }
        let title = NSMutableAttributedString(string: "")
        let font = NSFont.monospacedDigitSystemFont(ofSize: NSFont.systemFontSize(for: .small), weight: .medium)
        func append(_ text: String, _ color: NSColor = .labelColor) {
            title.append(NSAttributedString(string: text, attributes: [.font: font, .foregroundColor: color]))
        }
        let status: NSColor
        switch state.status {
        case "online": status = .systemGreen
        case "degraded": status = .systemOrange
        case "offline": status = .systemRed
        default: status = .secondaryLabelColor
        }
        append("● ", status)
        append(state.country)
        if state.showNet {
            append(" ↑" + state.up, .systemGreen)
            append(" ↓" + state.down, .systemRed)
        }
        if state.latency != "--" { append(" " + state.latency, .secondaryLabelColor) }
        for quote in state.quotes {
            append("  │  ", .secondaryLabelColor)
            let name = quote.ticker.hasPrefix("SSE:") || quote.ticker.hasPrefix("SZSE:") ? quote.name : quote.ticker
            append(name + " ")
            if let move = quote.percent, abs(move) >= 0.005 {
                append(percentText(move), (move > 0) == state.upIsRed ? .systemRed : .systemGreen)
            } else {
                append(percentText(quote.percent), .secondaryLabelColor)
            }
        }
        if state.quotes.isEmpty { append("  " + state.tr("等待排名", "Ranking…"), .secondaryLabelColor) }
        button.attributedTitle = title
        button.toolTip = "Samuel · " + state.tr("点击打开分时详情", "Click for intraday details")
        button.setAccessibilityValue(title.string)
    }
}

@main
@MainActor
enum SamuelApp {
    static func main() {
        if CommandLine.arguments.contains("--version") {
            let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "development"
            print("Samuel \(version)")
            return
        }
        if CommandLine.arguments.contains("--check-feed") {
            guard let line = readLine(), let data = line.data(using: .utf8),
                  let state = try? JSONDecoder().decode(Frame.self, from: data),
                  state.groups.contains(where: { $0.id == state.group }) else { exit(1) }
            return
        }
        signal(SIGPIPE, SIG_IGN)
        let app = NSApplication.shared
        let delegate = SamuelDelegate()
        app.setActivationPolicy(.accessory)
        app.delegate = delegate
        withExtendedLifetime(delegate) { app.run() }
    }
}

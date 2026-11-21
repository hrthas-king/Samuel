import ServiceManagement
import SwiftUI

@MainActor
final class LoginItem: ObservableObject {
    @Published private(set) var status: SMAppService.Status = .notRegistered
    @Published private(set) var errorMessage: String?

    private let service = SMAppService.mainApp

    var isRegistered: Bool {
        status == .enabled || status == .requiresApproval
    }

    func refresh() {
        let current = service.status
        if current != status {
            status = current
            errorMessage = nil
        }
    }

    func setRegistered(_ registered: Bool) {
        refresh()
        errorMessage = nil
        guard isRegistered != registered else { return }
        do {
            if registered {
                try service.register()
            } else {
                try service.unregister()
            }
        } catch {
            errorMessage = error.localizedDescription
        }
        refresh()
    }
}

@MainActor
struct LoginItemSetting: View {
    let english: Bool
    @StateObject private var item = LoginItem()

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Toggle(english ? "Launch at login" : "登录时启动", isOn: Binding(
                get: { item.isRegistered }, set: { item.setRegistered($0) }
            ))
            .toggleStyle(.checkbox)
            if item.status == .requiresApproval {
                HStack {
                    Text(english ? "Approval needed in System Settings" : "需在系统设置中允许")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Button(english ? "Open Login Items" : "打开登录项") {
                        SMAppService.openSystemSettingsLoginItems()
                    }
                }
            }
            if let error = item.errorMessage {
                Text((english ? "Couldn’t update the login item. " : "无法更改登录项。") + error)
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        }
        .onAppear { item.refresh() }
    }
}

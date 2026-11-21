import SwiftUI

@MainActor
struct SentinelSettingsView: View {
    struct Values: Decodable {
        var backend = "api", baseURL = "", model = ""
        var agyPath = "", agyModel = ""
        var opencodePath = "", opencodeModel = ""
        var codexPath = "", codexModel = ""
        var claudePath = "", claudeModel = ""
        var hasFREDKey = false, hasAPIKey = false, environmentOverrides = false
    }
    struct ModelOption: Decodable, Identifiable {
        let id: String
        let label: String
    }
    let english: Bool
    let back, saved: () -> Void
    @ViewState private var values = Values()
    @ViewState private var fred = ""
    @ViewState private var api = ""
    @ViewState private var failure = ""
    @ViewState private var clearFred = false
    @ViewState private var clearAPI = false
    @ViewState private var loaded = false
    @ViewState private var saving = false
    @ViewState private var discoveredModels: [String: [ModelOption]] = [:]
    @ViewState private var loadingModelsFor: Set<String> = []

    private func tr(_ zh: String, _ en: String) -> String { english ? en : zh }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Button(action: back) { Image(systemName: "chevron.backward") }
                    .accessibilityLabel(tr("返回", "Back")).disabled(saving)
                Text(tr("数据与模型设置", "Data and model settings")).font(.headline)
                Spacer()
                if saving || (!loaded && failure.isEmpty) { ProgressView().controlSize(.small) }
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    Text(tr("FRED 免费密钥", "FRED API key")).font(.headline)
                    secretField("FRED_API_KEY", $fred, stored: values.hasFREDKey, clear: $clearFred)
                    Text(tr("留空会自动读取 FRED 公开 CSV。", "With no key, Samuel uses FRED’s public CSV.")).font(.caption).foregroundStyle(.secondary)
                    Divider()
                    Picker(tr("研判方式", "Analysis backend"), selection: $values.backend) {
                        Text(tr("API（OpenAI 兼容）", "API (OpenAI-compatible)")).tag("api")
                        Text("Antigravity CLI").tag("agy")
                        Text("OpenCode CLI").tag("opencode")
                        Text("Codex CLI").tag("codex")
                        Text("Claude CLI").tag("claude")
                    }.pickerStyle(.menu)
                    if values.backend == "api" {
                        Text(tr("API 提供商地址", "API provider URL")).font(.caption)
                        TextField("https://api.openai.com/v1", text: $values.baseURL)
                        Text(tr("模型名称", "Model")).font(.caption)
                        TextField(tr("提供商的模型 ID", "Provider model ID"), text: $values.model)
                        Text(tr("模型密钥", "API key")).font(.caption)
                        secretField("API Key", $api, stored: values.hasAPIKey, clear: $clearAPI)
                    } else {
                        HStack(spacing: 6) {
                            Text(tr("模型选项", "Model")).font(.caption)
                            Spacer()
                            if loadingModelsFor.contains(values.backend) {
                                ProgressView().controlSize(.mini)
                            }
                            let options = cliModelOptions
                            if !options.isEmpty {
                                Menu(tr("选择模型", "Choose model")) {
                                    Button((cliModelBinding.wrappedValue.isEmpty ? "✓ " : "") + tr("使用 CLI 默认模型", "Use CLI default")) {
                                        cliModelBinding.wrappedValue = ""
                                    }
                                    Divider()
                                    ForEach(options) { option in
                                        Button {
                                            cliModelBinding.wrappedValue = option.id
                                        } label: {
                                            let prefix = cliModelBinding.wrappedValue == option.id ? "✓ " : ""
                                            Text(prefix + (option.label == option.id ? option.id : "\(option.label) (\(option.id))"))
                                        }
                                    }
                                }
                                .controlSize(.small)
                            }
                            if values.backend == "agy" || values.backend == "opencode" {
                                Button {
                                    Task { await loadCLIModelsIfNeeded(force: true) }
                                } label: {
                                    Image(systemName: "arrow.clockwise")
                                }
                                .buttonStyle(.borderless)
                                .controlSize(.small)
                                .disabled(loadingModelsFor.contains(values.backend))
                                .help(tr("重新读取此 CLI 的可用模型列表", "Reload available models from this CLI"))
                                .accessibilityLabel(tr("刷新模型列表", "Refresh model list"))
                            }
                        }
                        TextField(cliModelPlaceholder, text: cliModelBinding)
                        Text(tr("可执行文件路径", "Executable path")).font(.caption)
                        TextField(tr("留空自动查找", "Leave blank to locate automatically"),
                                  text: cliPathBinding)
                        Text(tr("模型留空时使用此 CLI 的默认模型，路径留空自动查找。请先在终端完成对应 CLI 登录。",
                                "Leave model blank for the CLI default and path blank to locate automatically. Sign in from Terminal first."))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    if values.environmentOverrides {
                        Text(tr("当前进程的非空环境变量优先于已保存设置。", "Non-empty environment variables override saved settings.")).font(.caption).foregroundStyle(.secondary)
                    }
                }.disabled(!loaded || saving).frame(maxWidth: .infinity, alignment: .leading)
            }
            if !failure.isEmpty { Text(failure).foregroundStyle(.red).font(.callout) }
            Button(tr("保存设置", "Save settings")) { Task { await save() } }
                .buttonStyle(.borderedProminent).disabled(!loaded || saving)
        }.padding(16)
        .task {
            do {
                values = try JSONDecoder().decode(Values.self, from: await SentinelHelper.output(["settings", "get"]))
                loaded = true
                await loadCLIModelsIfNeeded()
            } catch { failure = error.localizedDescription }
        }
        .onChange(of: values.backend) { _ in
            Task { await loadCLIModelsIfNeeded() }
        }
    }

    private func secretField(_ name: String, _ value: Binding<String>, stored: Bool, clear: Binding<Bool>) -> some View {
        HStack {
            SecureField(stored ? tr("已配置，留空保留", "Configured · leave blank to keep") : name, text: value)
                .accessibilityLabel(name).disabled(clear.wrappedValue)
            if stored { Toggle(tr("清除", "Clear"), isOn: clear).toggleStyle(.checkbox) }
        }
    }

    private var cliPathBinding: Binding<String> {
        switch values.backend {
        case "agy": return $values.agyPath
        case "opencode": return $values.opencodePath
        case "codex": return $values.codexPath
        default: return $values.claudePath
        }
    }

    private var cliModelBinding: Binding<String> {
        switch values.backend {
        case "agy": return $values.agyModel
        case "opencode": return $values.opencodeModel
        case "codex": return $values.codexModel
        default: return $values.claudeModel
        }
    }

    private var cliModelPlaceholder: String {
        switch values.backend {
        case "agy": return tr("留空用默认，如 gemini-3.8-flash-high", "Default if blank, e.g. gemini-3.8-flash-high")
        case "opencode": return tr("留空用默认，如 opencode/deepseek-v4.1-flash-free", "Default if blank, e.g. opencode/deepseek-v4.1-flash-free")
        case "codex": return tr("留空用默认，如 gpt-5", "Default if blank, e.g. gpt-5")
        default: return tr("留空用默认，如 sonnet / opus", "Default if blank, e.g. sonnet / opus")
        }
    }

    private var cliModelOptions: [ModelOption] {
        if let found = discoveredModels[values.backend], !found.isEmpty {
            return found
        }
        if values.backend == "claude" {
            return [
                ModelOption(id: "sonnet", label: "Claude Sonnet"),
                ModelOption(id: "opus", label: "Claude Opus"),
                ModelOption(id: "haiku", label: "Claude Haiku"),
            ]
        }
        return []
    }

    private func loadCLIModelsIfNeeded(force: Bool = false) async {
        let tool = values.backend
        guard loaded, (tool == "agy" || tool == "opencode"), !loadingModelsFor.contains(tool) else { return }
        if !force, discoveredModels[tool] != nil { return }
        loadingModelsFor.insert(tool)
        defer { loadingModelsFor.remove(tool) }
        var args = ["models", tool]
        let custom = cliPathBinding.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if !custom.isEmpty { args.append(custom) }
        if let data = try? await SentinelHelper.output(args),
           let list = try? JSONDecoder().decode([ModelOption].self, from: data) {
            discoveredModels[tool] = list
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        var changes = [
            "backend": values.backend, "baseURL": values.baseURL, "model": values.model,
            "agyPath": values.agyPath, "agyModel": values.agyModel,
            "opencodePath": values.opencodePath, "opencodeModel": values.opencodeModel,
            "codexPath": values.codexPath, "codexModel": values.codexModel,
            "claudePath": values.claudePath, "claudeModel": values.claudeModel,
        ]
        if clearFred || !fred.isEmpty { changes["fredKey"] = clearFred ? "" : fred }
        if clearAPI || !api.isEmpty { changes["apiKey"] = clearAPI ? "" : api }
        do {
            _ = try await SentinelHelper.output(["settings", "set"], input: JSONEncoder().encode(changes))
            fred = ""; api = ""
            saved()
        } catch { failure = error.localizedDescription }
    }
}

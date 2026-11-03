# 调度与架构设计

```mermaid
flowchart LR
    NIC["出口路由 + GetIfEntry2\nTUN 时选择底层默认路由\n每秒读取单一接口计数"] --> Samples["采样通道"]
    ICMP["IcmpSendEcho\n32 字节 payload / 每秒一包"] --> Probes["探针通道"]
    HTTPS["独立 WinHTTP HTTPS GET\n每 5 秒采样 / 新连接"] --> Latency["延迟结果 + 出口代次"]
    Events["NotifyIpInterfaceChange\nNotifyUnicastIpAddressChange\nNotifyRouteChange2"] --> Debounce["事件信号 / 去抖 / 网络代次"]
    Egress["WatchEgress\n本地 TUN / 代理端点指纹"] --> Debounce
    Samples --> Engine["单 goroutine 状态聚合引擎"]
    Probes --> Engine
    Latency --> Engine
    Debounce --> Engine
    Menu["悬浮条 / 托盘菜单命令"] --> Engine
    Custom["其他 / 原生无边框输入浮窗"] --> Validate["后台股票校验 / 面板代次"]
    Validate --> Saved["config.json 原子保存列表与选择"]
    Saved --> Registry["自定义组注册 / 锁保护快照"]
    Registry --> Engine
    Engine --> GeoGate["冷却限流 / 网络代次校验"]
    GeoGate --> Geo["后台 WinHTTP\nCloudflare trace 解析"]
    Geo --> Engine
    Engine --> QuoteGate["指数退避 / 2~5s 轮询"]
    QuoteGate --> Quotes["后台 WinHTTP\nVOO / QQQ 24h 实时流校验"]
    Quotes --> Engine
    Engine --> Mailbox["原子快照 + 合并 PostMessage"]
    Mailbox --> UI["锁定 OS 线程\nGDI 任务栏悬浮条 + 系统托盘"]
```

## 1. 核心职责分离

1. **单线程 Win32 UI 层 (`internal/ui`)**：
   - 锁定主 OS 线程，同时承载 **任务栏分层置顶悬浮条 (`WS_EX_LAYERED | WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE`)** 与 **系统托盘动态图标 (`Shell_NotifyIconW`)**。
   - 锁定时启用 `WS_EX_TRANSPARENT` 鼠标穿透；解锁时支持左键拖拽位置与 `tooltips_class32` 原生悬停提示。
   - 不在 UI 线程执行任何 ICMP、WinHTTP、注册表或磁盘写入。
2. **固定 1 秒网络心跳与网卡采样 (`internal/network` + `internal/monitor`)**：
   - `GetBestInterface` + `GetIfEntry2` 查找当前路由接口；TUN / VPN 接口会通过 `GetIpForwardTable` 的默认路由及其综合 metric 选择一个可用的非隧道底层接口，同等优先级优先硬件接口。不相加多个接口，不采用断开、过滤或回环接口。
   - 用选中接口的 64 位收发字节和实际采样间隔计算速率，LUID / 索引切换或计数回退时重新建立基线。连通性探针单独检查原始路由，统计接口不可用不直接导致连通性探针本地错误。
   - 每秒对 `1.1.1.1` / `8.8.8.8` 发起 ICMP 心跳；主目标失败时同周期复核备用目标，必要时通过 TCP 443 和最近 HTTPS 通信/流量作连通性旁证。
   - 已在线时单次失败保持绿色；连续 2～3 次失败进入 `Degraded`，连续 4 次失败进入 `Offline`，一次成功恢复 `Online`。旁证只参与连通判断，不生成或沿用延迟数值。
3. **独立 HTTPS 延迟 (`internal/network/latency_windows.go`)**：
   - 每 5 秒以新 WinHTTP 连接请求 `https://www.cloudflare.com/cdn-cgi/trace`，继承系统代理与 TLS 验证；计时从发送前到收到响应头，包含 DNS、建连和 TLS。
   - `Snapshot.Probe` 保存连通性证据；`Snapshot.Latency` 单独保存 HTTPS 耗时、目标、方法、采样时间和错误。UI 仅展示新鲜有效的 HTTPS 延迟。
   - 出口/路由变化或手动刷新时递增独立延迟代次，取消旧请求并立即重测；迟到的旧代次结果不允许覆盖新出口。失败或超过 15 秒未更新时显示 `--`。
4. **事件驱动公网 IP 与国家定位 (`internal/ipinfo` + `internal/network/egress_windows.go`)**：
   - 监听系统网卡、IP 地址、路由变更通知，750ms 事件去抖。
   - 每秒本地检测系统代理、TUN 网卡与常见代理内核（`mihomo`、`sing-box`、`xray`、`clash` 等）的远端连接指纹，连续稳定 2 秒后触发复核。
   - 使用 WinHTTP 请求 `https://www.cloudflare.com/cdn-cgi/trace`，自动继承系统代理与系统 TLS 根证书，禁用 Keep-Alive 确保探测新节点出口。
5. **全球多市场行情 (`internal/market`)**：
   - 支持 11 组内置标的及本机自定义组合。美股与欧洲锁定 Yahoo，中国市场及日韩锁定腾讯，黄金/布油保留腾讯期货接口；失败不跨提供方切换。Yahoo v7 失败后可用同源 v8 图表常规报价。
   - `sessions.go` 对所有内置及自定义美股股票/ETF 选择当前盘前、盘中、盘后、夜盘的同组字段。常规涨跌以前收为基准，延长时段以该组价格减涨跌额得到的对应基准校验百分比；仅浮点尾差与常规参考价合并，避免独立字段差异误退回。`overnight.go` 提供纽约交易周的夜盘窗口。缺失、跨时段、明显未来或不一致数据明确回退到灰色的最近常规报价；v8 全天价不冒充延长时段。缓存视图在时段边界立即失效。现金指数不使用其期货或 ETF 代理，不伪造延长时段。
   - `commodity_sessions.go` 分别使用内嵌纽约/伦敦 IANA 时区判断黄金/布油交易与维护窗口；只在源时间属于本次交易周期时点亮。中国竞价需有本日有效时间及价格；指数午休、收盘、日韩现金市场边界单独处理。个股在延长时段对来源公司总市值按显示价估算并标记。
   - 行情采用独立轮询、失败退避、缓存与过期判断；行情请求耗时不会充当网络延迟。

6. **自定义股票对 (`internal/watchlist`, `config`, `market/custom*`, `ui/custom*`)**：
   - 六位数字保留前导零，标准化为沪/深/北交所内部代码；通过腾讯行情的代码与股票品类核实有效性，使用 Win32 解码中文名称。
   - 列表与当前组存于同一配置文件。只有磁盘保存成功后才替换注册表、菜单状态并通知聚合引擎刷新。
   - 校验任务携带面板代次；关闭/重开浮窗后，迟到结果不能保存到新面板。组切换通道保留最新选择，删除活动组时立即切回默认组。

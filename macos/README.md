# Samuel for macOS

Samuel 在 Mac 菜单栏显示网络状态和两只标的的涨跌幅。点击行情条可查看分时图、盘口和资讯，也可从「宏观」打开美股宏观情绪面板。

## 下载与升级

**1.3.2** 适用于 **macOS 13 或更新版本**。Apple Silicon 和 Intel Mac 共用一个安装包。

1. 在 [macOS 1.3.2 发布页](https://github.com/hrthas-king/Samuel/releases/tag/macos-v1.3.2) 下载 `Samuel-1.3.2-macos-universal.zip`。
2. 如果 Samuel 正在运行，点击菜单栏行情条，在齿轮菜单中选「退出 Samuel」。
3. 解压 ZIP，将 `Samuel.app` 拖到 Finder 的「应用程序」。升级时在 Finder 提示中选择替换，然后打开 Samuel。

发布包采用临时签名（ad hoc），Apple 公证状态可在发布页核对。压缩包校验值见同页的 `SHA256SUMS-macos.txt`。下载两个文件后，可在终端进入所在目录运行 `shasum -a 256 -c SHA256SUMS-macos.txt`。

## 查看行情

- 点击菜单栏打开面板，从顶部选择行情分组。默认组合是 VOO 和 QQQ。美股和欧洲指数使用 Yahoo Finance，中国内地、香港、日韩和商品组合使用腾讯行情。
- 点击任一标的查看分时。悬停显示具体时刻、价格和涨跌幅，单击固定。分时图按北京时间 10:00 至次日 09:00 绘制，报价时间按交易所时区显示。
- 点击圆箭头请求刷新。齿轮里可调整刷新间隔、界面语言、涨跌配色和自定义组合，也可启用「登录时启动」。
- 「宏观」提供风险温度、VIX 和 CNN 恐惧与贪婪指数，以及历史信号回填。配置方式见 [美股宏观功能说明](../README.md#macos-美股宏观情绪与择时)。

## 行情更新与故障排查

1.3.2 在 Yahoo 认证报价失败或只返回一个标的时，并行补取缺失标的的同源公开图表报价。备用接口显示可核实的常规报价，详情会标注上次报价的时段和时间。Yahoo 返回 HTTP 429 时，常规行情轮询等待 30 秒再请求。

| 菜单栏或详情提示 | 含义与操作 |
| --- | --- |
| `–` | 当前标的还没有可显示的报价。点击标的，在详情里查看请求错误或等待提示。 |
| 灰色涨跌幅与 `!` | 正在等待更新，菜单栏保留上次可核实的报价。详情显示报价时间和等待原因。 |
| 「常规收盘」 | 当前显示最近可核实的常规交易报价。盘前、盘后或夜盘数据可用后，面板会随下一轮更新。 |
| `HTTP 429` | 数据源请求受限。等待自动重试即可。 |

菜单栏的延迟测的是网络探测请求。排查某只标的时，请以它详情里的**来源、报价时间和错误提示**为准。

## 从源码构建

需要 macOS、Xcode 命令行工具和 Go 1.24 或更新版本。从 [macOS 1.3.2 标签](https://github.com/hrthas-king/Samuel/tree/macos-v1.3.2) 取得源码，在仓库根目录执行。

```sh
go test ./...
bash scripts/build-macos.sh
```

通用安装包输出到 `dist/Samuel-1.3.2-macos-universal.zip`。应用设置与宏观快照保存在 `~/Library/Application Support/Samuel/`。

[更新记录](../docs/CHANGELOG.md#macos-132) · [项目首页](../README.md)

// Package locale translates presentation text without changing quote data.
package locale

import (
	"sort"
	"strings"
)

const Chinese = "zh-CN"
const English = "en"

func Normalize(language string) string {
	if language == English {
		return English
	}
	return Chinese
}

func Valid(language string) bool { return language == Chinese || language == English }

// Detail translates whole chart captions. Common short labels stay out of
// the diagnostic text replacer, where they could alter unrelated text.
func Detail(language, key string) string {
	if Normalize(language) != English {
		return key
	}
	switch key {
	case "悬停取点 · 点击固定":
		return "Hover to inspect · click to pin"
	case "上次常规盘收盘":
		return "Last regular close"
	case "盘口与基本面":
		return "Market data & fundamentals"
	case "今开":
		return "Open"
	case "最高":
		return "High"
	case "最低":
		return "Low"
	case "前收":
		return "Prev close"
	case "成交量":
		return "Volume"
	case "成交额":
		return "Turnover"
	case "52 周区间":
		return "52W range"
	case "52 周区间取自腾讯日线":
		return "52W range from Tencent daily bars"
	case "日内振幅":
		return "Day range %"
	case "手":
		return "lots"
	case "常规盘盘口":
		return "Regular-session data"
	case "美元总市值":
		return "Market cap (USD)"
	case "预期市盈率":
		return "Forward PE"
	case "基本 EPS":
		return "Basic EPS"
	case "EPS 报告期":
		return "EPS period"
	case "东方财富":
		return "East Money"
	case "指数 PE":
		return "Index PE"
	case "指数 PB":
		return "Index PB"
	case "括号内为历史分位（最长近 10 年）":
		return "Percentiles in parentheses (up to 10 years)"
	case "跟踪标普 500 指数":
		return "Tracks the S&P 500 Index"
	case "跟踪纳斯达克 100 指数":
		return "Tracks the Nasdaq-100 Index"
	case "跟踪 NYSE Semiconductor Index":
		return "Tracks the NYSE Semiconductor Index"
	case "跟踪 MVIS US Listed Semiconductor 25 Index":
		return "Tracks the MVIS US Listed Semiconductor 25 Index"
	case "COMEX 黄金期货（腾讯报价）":
		return "COMEX gold futures (Tencent quote)"
	case "ICE 布伦特原油期货（腾讯报价）":
		return "ICE Brent crude futures (Tencent quote)"
	case "正在读取盘口…":
		return "Loading market data…"
	case "财报日期":
		return "Earnings date"
	case "下一期财报":
		return "Next earnings"
	case "最近实际披露":
		return "Latest reported earnings"
	case "最近财报日期":
		return "Latest earnings date"
	case "暂无可核实的财报日期":
		return "Verified earnings date unavailable"
	case "正在读取财报日期…":
		return "Loading earnings date…"
	case "仅日期":
		return "date only"
	case "预计":
		return "estimated"
	case "预约披露":
		return "scheduled disclosure"
	case "剩余 %d 天":
		return "in %d days"
	case "今日":
		return "Today"
	case "剩余 %s":
		return "in %s"
	case "关键宏观与议息节点":
		return "Macro & rate decisions"
	case "FOMC 利率决议":
		return "FOMC rate decision"
	case "FOMC 经济预测与点阵图":
		return "FOMC projections & dot plot"
	case "美联储主席新闻发布会":
		return "Fed Chair press conference"
	case "FOMC 会议纪要":
		return "FOMC minutes"
	case "非农就业":
		return "nonfarm payrolls"
	case "非农":
		return "NFP"
	case "失业率":
		return "unemployment rate"
	case "核心 CPI":
		return "core CPI"
	case "核心 PCE":
		return "core PCE"
	case "初请失业金":
		return "initial jobless claims"
	case "零售销售":
		return "retail sales"
	case "ISM 制造业 PMI":
		return "ISM manufacturing PMI"
	case "ISM 服务业 PMI":
		return "ISM services PMI"
	case "欧洲央行利率决议":
		return "ECB rate decision"
	case "欧洲央行存款利率":
		return "ECB deposit facility rate"
	case "日本央行利率决议":
		return "BOJ rate decision"
	case "韩国央行利率决议":
		return "BOK rate decision"
	case "一年期 LPR":
		return "1-year China LPR"
	case "五年期 LPR":
		return "5-year China LPR"
	case "制造业 PMI":
		return "manufacturing PMI"
	case "非制造业 PMI":
		return "non-manufacturing PMI"
	case "社会融资规模":
		return "China aggregate financing"
	case "EIA 原油库存":
		return "EIA crude inventories"
	case "OPEC+ 产油政策会议":
		return "OPEC+ production policy meeting"
	case "美国":
		return "US"
	case "中国":
		return "China"
	case "欧元区":
		return "Euro area"
	case "日本":
		return "Japan"
	case "韩国":
		return "South Korea"
	case "德国":
		return "Germany"
	case "法国":
		return "France"
	case "同比":
		return "YoY"
	case "环比":
		return "MoM"
	case "较上季":
		return "QoQ"
	case "实际":
		return "Actual"
	case "预期":
		return "Forecast"
	case "前值":
		return "Prior"
	case "实际值未提供":
		return "Actual unavailable"
	case "加息":
		return "raised"
	case "降息":
		return "cut"
	case "维持利率":
		return "held"
	case "下一次 %s":
		return "Next %s"
	case "正在读取宏观日历…":
		return "Loading macro calendar…"
	case "暂无匹配的高影响事件":
		return "No matching high-impact events"
	case "宏观日历暂不可用":
		return "Macro calendar unavailable"
	case "财报与关联资讯":
		return "Earnings & related news"
	case "正在读取关联资讯…":
		return "Loading related news…"
	case "暂无匹配的近期资讯":
		return "No matching recent news"
	case "资讯源暂不可用":
		return "News feed unavailable"
	case "议息":
		return "Rates"
	case "宏观":
		return "Macro"
	case "财报":
		return "Earnings"
	case "时政":
		return "Policy"
	case "公司":
		return "Company"
	case "市场":
		return "Market"
	default:
		return Text(language, key)
	}
}

var english = func() map[string]string {
	entries := [][2]string{
		{"采样", "sampling"},
		{"（盘前涨跌基准）", " (pre-market reference)"},
		{"（盘后涨跌基准）", " (after-hours reference)"},
		{"（夜盘涨跌基准）", " (overnight reference)"},
		{"涨跌 ", "Change "},
		{"数据日期", "Data date"},
		{"分位 ", "Percentile "},
		{"实际历史", "Actual history"},
		{" 至 ", " to "},
		{"个有效样本", " valid samples"},
		{"缺少历史或有效样本不足30", "History missing or fewer than 30 valid samples"},
		{"请输入6位沪深北股票/ETF代码，或美股代码（如AAPL）", "Enter a 6-digit China stock/ETF code or a U.S. ticker (e.g. AAPL)"},
		{"请输入6位数字或有效的美股字母代码", "Enter 6 digits or a valid U.S. ticker"},
		{"不支持该代码号段，请输入沪深北股票或场内ETF代码", "Unsupported code prefix; enter a China-listed stock or ETF"},
		{"请输入两个不同的股票或ETF代码", "Enter two different stock or ETF symbols"},
		{"股票1：", "Symbol 1: "},
		{"股票2：", "Symbol 2: "},
		{"不存在该股票或行情代码不匹配", "Instrument not found or quote identity mismatch"},
		{"未找到该标的，请核对代码", "Instrument not found; check the symbol"},
		{"未找到该股票，请核对代码或新旧代码", "Stock not found; check the current symbol"},
		{"股票名称无效", "Invalid instrument name"},
		{"股票价格或行情时间无效", "Invalid quote price or time"},
		{"未获取到有效股票或ETF行情", "No valid stock or ETF quotes received"},
		{"未获取到有效A股或ETF行情", "No valid China stock or ETF quotes received"},
		{"重复的自定义组合", "Duplicate custom pair"},
		{"重复的股票行情", "Duplicate instrument response"},
		{"腾讯前收", "Tencent previous close"},
		{"腾讯涨跌额对应基准", "Tencent change reference"},
		{"腾讯期货价格基准", "Tencent futures reference"},
		{"总市值（亿元人民币）", "total market cap (CNY 100 million)"},
		{"总市值", "total market cap"},
		{"延迟约", "Delayed ~"},
		{"参考", "Reference"},
		{"股叉叉（同指数历史序列）", "Guchacha (same-index history)"},
		{"腾讯财经（同指数行情字段）", "Tencent Finance (same-index quote fields)"},
		{"PE-TTM / PB，指数整体口径；分位为近10年有效样本中不高于当前值的比例，历史不足10年按实际区间，少于30个样本不计算", "Index-level PE-TTM / PB; percentile is the share of valid samples no higher than the current value over up to 10 years; actual coverage is used, with at least 30 samples required"},
		{"数据源 PE/PB 口径；未提供可核实历史，百分位缺失", "Provider PE/PB convention; verified history and percentiles unavailable"},
		{"暂无可核实的同指数数据，未使用全国市场或ETF代理", "No verified same-index data; no country-market or ETF proxy is used"},
		{"指数估值（括号内为历史百分位）", "Index valuation (historical percentile in parentheses)"},
		{"指数估值:", "Index valuation:"},
		{"历史分位", "Historical percentile"},
		{"暂无可核实的同指数历史来源", "Verified same-index history unavailable"},
		{"未配置可核实的同指数历史来源", "No verified same-index history configured"},
		{"尚未读取", "Not read yet"},
		{"最后更新于%s", "Last updated %s"},
		{"源未提供行情时间", "Quote time unavailable from source"},
		{"正在读取分时数据…", "Loading intraday data…"},
		{"暂无可核实的分时数据", "Verified intraday data unavailable"},
		{"等待报价…", "Waiting for a quote…"},
		{"等待市值排名", "Waiting for ranking"},
		{"本次打开后采样", "Quotes sampled since opening"},
		{"暂无数据", "Unavailable"},
		{"有效样本", "valid samples"},
		{"（含 DNS、建连、TLS、响应头）", " (DNS, connection, TLS and response headers included)"},
		{"早盘/盘前(黄)", "Pre-market (yellow)"},
		{"盘中(绿)", "Regular (green)"},
		{"盘后(蓝)", "After-hours (blue)"},
		{"夜盘(紫)", "Overnight (purple)"},
		{"休市/周末(灰)", "Closed/weekend (gray)"},
		{"语言 / Language", "Language / 语言"},
		{"立即刷新 IP 与行情", "Refresh IP and quotes now"},
		{"分时详情 · ", "Intraday chart · "},
		{"任务栏显示实时网速与延迟", "Show network speed and latency"},
		{"锁定位置（鼠标穿透）", "Lock position (click-through)"},
		{"移到任务栏最左侧", "Move to the left of the taskbar"},
		{"涨跌配色：绿涨红跌", "Price colors: green up / red down"},
		{"涨跌配色：红涨绿跌", "Price colors: red up / green down"},
		{"Samuel 应用图标", "Samuel application icon"},
		{"网络状态图标（地区 / 上下行）", "Network status icon (region / traffic)"},
		{"托盘图标（当前：", "Tray icon (current: "},
		{"应用图标", "Application icon"},
		{"网络状态", "Network status"},
		{"More（更多行情）", "More (markets)"},
		{"显示大小 (当前: ", "Display size (current: "},
		{"刷新间隔（当前 ", "Refresh interval (current: "},
		{"75% · 迷你", "75% · Mini"},
		{"85% · 紧凑", "85% · Compact"},
		{"90% · 适中", "90% · Medium"},
		{"100% · 标准", "100% · Standard"},
		{"115% · 放大", "115% · Large"},
		{"130% · 特大", "130% · Extra large"},
		{"查看详细诊断信息", "View detailed diagnostics"},
		{"开机自启", "Start with Windows"},
		{"退出", "Exit"},
		{"TUN/代理活跃：地区每 30 秒自动复核", "TUN/proxy active: exit region checked every 30 seconds"},
		{"+ 新增组合", "+ Add pair"},
		{"删除该组合", "Remove pair"},
		{"操作", "Actions"},
		{"其他（自定义股票 / ETF）", "Other (custom stocks / ETFs)"},
		{"新增自定义股票 / ETF组合", "Add a custom stock / ETF pair"},
		{"新增自定义组合", "Add custom pair"},
		{"支持沪深北股票、场内 ETF 和美股", "China-listed stocks and ETFs, and U.S. stocks"},
		{"代码 1", "Symbol 1"},
		{"代码 2", "Symbol 2"},
		{"取消", "Cancel"},
		{"添加并显示", "Add and show"},
		{"例如：513100 + 513500 / AAPL + MSFT\nEnter 添加 · Esc 或点击外部关闭", "Example: 513100 + 513500 / AAPL + MSFT\nEnter: add · Esc / click outside: close"},
		{"6位数字或美股代码，如 AAPL", "6 digits or a U.S. ticker, e.g. AAPL"},
		{"正在核实股票 / ETF代码…", "Checking stock / ETF symbols…"},
		{"请输入有效的A股或场内ETF代码", "Enter a valid China-listed stock or ETF code"},
		{"请输入6位沪深北股票或ETF代码，或美股代码（如 AAPL）", "Enter a 6-digit China stock/ETF code or a U.S. ticker (e.g. AAPL)"},
		{"该组合已存在", "This pair already exists"},
		{"该组合不存在", "This pair does not exist"},
		{"两个股票代码不能相同", "The two symbols must be different"},
		{"无法创建输入控件", "Could not create input controls"},
		{"保存组合失败", "Could not save the pair"},
		{"保存语言设置失败", "Could not save the language setting"},
		{"保存托盘图标设置失败", "Could not save the tray icon setting"},
		{"Samuel — 网络与多市场行情诊断", "Samuel — Network and market diagnostics"},
		{"Samuel 错误", "Samuel error"},
		{"查询中/未知", "Checking / unknown"},
		{"未知/查询中", "Unknown / checking"},
		{"未知网卡", "Unknown adapter"},
		{"断网警告", "Offline"},
		{"待确认", "Checking"},
		{"检测中", "Checking"},
		{"正常", "Online"},
		{"未知", "Unknown"},
		{"出口 ", "Exit "},
		{"网络: ", "Network: "},
		{"行情: ", "Markets: "},
		{"…(见诊断)", "…(details)"},
		{"…*(见诊断)", "…*(details)"},
		{"【网络】", "[Network]"},
		{"【行情】", "[Markets]"},
		{"网卡:", "Adapter:"},
		{"速率:", "Speed:"},
		{"HTTPS 延迟:", "HTTPS latency:"},
		{"上行待确认", "Upload unverified"},
		{"上下行已验证", "Traffic verified"},
		{"本地发送错误", "Local send error"},
		{"延迟目标:", "Latency target:"},
		{"等待测量", "Waiting for measurement"},
		{" 秒采样（含建连与 TLS）", " seconds (connection and TLS included)"},
		{"每 ", "Every "},
		{"延迟测量提示:", "Latency note:"},
		{"模式: 检测到 TUN / 系统代理活跃（30s 自动复核出口）", "Mode: TUN / system proxy active (exit checked every 30s)"},
		{"注：时段信号 ☀早盘/盘前(黄) | 🟢盘中(绿) | 🔵盘后(蓝) | 🌙夜盘(紫) | ⚪休市(灰)（异常时右侧显示 !）。", "Sessions: ☀ pre-market | 🟢 regular | 🔵 after-hours | 🌙 overnight | ⚪ closed (! indicates a data warning)."},
		{"GeoPulse 网络与出口监控", "Network and exit monitoring"},
		{"连通状态:", "Connectivity:"},
		{"连续失败:", "Consecutive failures:"},
		{"出口国家:", "Exit country:"},
		{"公网 IP:", "Public IP:"},
		{"出口网卡:", "Exit adapter:"},
		{"TUN/代理活跃:", "TUN/proxy active:"},
		{"实时速率:", "Network speed:"},
		{"连通性探针:", "Connectivity probe:"},
		{"上行证据", "Upload evidence"},
		{"目标 ", "Target "},
		{"HTTPS 请求延迟:", "HTTPS request latency:"},
		{"延迟采样时间", "Latency sampled at"},
		{"定位提示:", "Location note:"},
		{"网卡提示:", "Adapter note:"},
		{"多市场行情与五态时段监控", "Markets and trading sessions"},
		{"时段信号说明:", "Session legend:"},
		{"标的名称:", "Instrument:"},
		{"别名:", "Name:"},
		{"时段状态:", "Session:"},
		{"数据源:", "Source:"},
		{"模式:", "Mode:"},
		{"当前最新价:", "Latest price:"},
		{"常规盘收盘:", "Regular price:"},
		{"涨跌额:", "Change:"},
		{"涨跌幅:", "Change %:"},
		{"参考基准价:", "Reference:"},
		{"最近成交", "Last trade"},
		{"HTTP耗时:", "HTTP time:"},
		{"行情数据正常", "Quote data available"},
		{"实时数据正常", "Quote data available"},
		{"数据异常", "Data warning"},
		{"等待数据", "Waiting for data"},
		{"尚未成功读取", "Not received yet"},
		{" 前轮询", " since poll"},
		{"源报价时间", "Source quote time"},
		{"源未提供逐笔时间（轮询时间不代表行情时间）", "Source has no tick time (poll time is not quote time)"},
		{"源报价日期", "Source quote date"},
		{"纽约时间，仅日期，无逐笔时间", "New York date only; no tick time"},
		{"行情时间距今", "Quote age "},
		{"行情时间", "Quote time"},
		{"北京时间", "China time"},
		{" 前", " ago"},
		{"时段:", "Session:"},
		{"涨跌基准", "Change reference"},
		{"当前时段报价暂缺", "Current-session data unavailable"},
		{"保留最近核实报价", "Keeping the last verified quote"},
		{"收到较早行情", "Older quote received"},
		{"保留最新已核实报价", "Keeping the latest verified quote"},
		{"交易时段已切换，等待当前时段行情", "Session changed; waiting for current-session data"},
		{"超过预期刷新时间，数据源失联", "Quote refresh overdue; source unavailable"},
		{"盘前数据不可用", "Pre-market data unavailable"},
		{"盘后数据不可用", "After-hours data unavailable"},
		{"夜盘数据不可用", "Overnight data unavailable"},
		{"盘中报价未更新", "Regular quote not updated"},
		{"休市或报价未更新", "Closed or quote not updated"},
		{"显示Yahoo常规收盘", "Showing Yahoo regular close"},
		{"显示Yahoo最近常规报价", "Showing latest Yahoo regular quote"},
		{"显示Yahoo常规主报价", "Showing Yahoo regular headline"},
		{"显示Yahoo常规报价", "Showing Yahoo regular quote"},
		{"显示Yahoo最近指数报价", "Showing latest Yahoo index quote"},
		{"显示Yahoo盘前报价", "Showing Yahoo pre-market quote"},
		{"显示Yahoo盘后报价", "Showing Yahoo after-hours quote"},
		{"显示Yahoo夜盘报价", "Showing Yahoo overnight quote"},
		{"Yahoo最近常规收盘", "Yahoo latest regular close"},
		{"Yahoo常规交易前收", "Yahoo previous regular close"},
		{"Yahoo常规涨跌额对应基准", "Yahoo regular change reference"},
		{"价与涨跌额对应基准", " price/change reference"},
		{"涨跌额对应基准", " change reference"},
		{"来源口径", "Provider convention"},
		{"反推", "derived"},
		{"盘前", "Pre-market"},
		{"盘中", "Regular"},
		{"盘后", "After-hours"},
		{"夜盘", "Overnight"},
		{"休市", "Closed"},
		{"周末", "Weekend"},
		{"期货交易中", "Futures trading"},
		{"期货休市或维护时段", "Futures closed / maintenance"},
		{"期货报价未更新；显示最近报价", "Futures quote not updated; showing latest quote"},
		{"集合竞价报价未更新；显示最近报价", "Auction quote not updated; showing latest quote"},
		{"集合竞价", "Opening auction"},
		{"午间休市", "Lunch break"},
		{"前场交易", "Morning session"},
		{"后场交易", "Afternoon session"},
		{"收市竞价窗口（最迟16:10结束）", "Closing auction window (ends by 16:10)"},
		{"收市竞价报价尚未更新", "Closing auction quote not updated"},
		{"指数尚未开盘", "Index not open yet"},
		{"开市前时段", "Before opening"},
		{"指数报价尚未更新", "Index quote not updated"},
		{"菜单行情暂不可用", "Menu quotes unavailable"},
		{"尚无有效成交价；显示前收", "No valid trade price; showing previous close"},
		{"休市或当前时段报价未更新", "Closed or current-session quote not updated"},
		{"市值 ", "Mkt cap "},
		{"美元总市值:", "Market cap (USD):"},
		{"暂无数据（市值或换算汇率不可用）", "Unavailable (market cap or FX missing)"},
		{"来源:", "Source:"},
		{"按最新价或汇率估算", "Estimated from quote or FX"},
		{"汇率时间", "FX time"},
		{"PE/PB 暂无数据", "PE/PB unavailable"},
		{"暂无同指数可核实数据", "No verified data for this index"},
		{"统计日期", "As of"},
		{"历史区间", "History"},
		{"样本数", "Samples"},
		{"百分位", "Percentile"},
		{"估值", "Valuation"},
		{"纳指ETF", "Nasdaq ETF"},
		{"标普500ETF", "S&P 500 ETF"},
		{"纳指100", "Nasdaq 100"},
		{"标普500", "S&P 500"},
		{"上证", "Shanghai Composite"},
		{"深证成指", "Shenzhen Component"},
		{"深证", "Shenzhen"},
		{"创业板", "ChiNext"},
		{"恒生", "Hang Seng"},
		{"恒科", "Hang Seng Tech"},
		{"日经225", "Nikkei 225"},
		{"韩国综合", "KOSPI"},
		{"黄金", "Gold"},
		{"布油", "Brent"},
		{" 秒", " s"},
		{"：", ": "},
		{"；", "; "},
		{"（", " ("},
		{"）", ")"},
		{"。", "."},
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		result[entry[0]] = entry[1]
	}
	return result
}()

var replacer = func() *strings.Replacer {
	keys := make([]string, 0, len(english))
	for key := range english {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	pairs := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		pairs = append(pairs, key, english[key])
	}
	return strings.NewReplacer(pairs...)
}()

func Text(language, text string) string {
	if Normalize(language) != English {
		return text
	}
	if translated, ok := english[text]; ok {
		return translated
	}
	return replacer.Replace(text)
}

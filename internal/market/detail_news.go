package market

import (
	"encoding/json"
	"encoding/xml"
	"html"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

type NewsItem struct {
	Title, Source, URL, Category string
	Published                    time.Time
}

func compactFeedText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, html.UnescapeString(value))
	runes := []rune(strings.Join(strings.Fields(value), " "))
	return string(runes[:min(limit, len(runes))])
}

// SecureNewsURL is used on ingestion and again immediately before launching
// the browser. Publisher links are data from outside the application.
func SecureNewsURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 4096 || strings.ContainsAny(raw, "\\\"<>") || strings.ContainsFunc(raw, unicode.IsSpace) {
		return ""
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || strings.ContainsFunc(decoded, unicode.IsControl) || strings.ContainsRune(decoded, '\\') {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !strings.Contains(host, ".") || net.ParseIP(host) != nil || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.ContainsAny(host, "%:") {
		return ""
	}
	return u.String()
}

func rssArticleURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && strings.EqualFold(u.Hostname(), "www.bing.com") &&
		strings.EqualFold(u.Path, "/news/apiclick.aspx") {
		return SecureNewsURL(u.Query().Get("url"))
	}
	return SecureNewsURL(raw)
}

func newsCategory(title string, equity bool) string {
	title = strings.ToLower(title)
	contains := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(title, word) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("earnings", "quarterly results", "guidance", "财报", "季报", "年报", "业绩", "財報", "業績"):
		return "财报"
	case contains("tariff", "export control", "sanction", "geopolit", "hormuz", "opec", "war ", "关税", "關稅", "制裁", "出口管制", "监管", "監管", "地缘", "地緣", "冲突", "海峡", "海峽"):
		return "时政"
	case contains("fomc", "federal reserve", "fed ", "rate cut", "rate hike", "ecb ", "美联储", "美聯儲", "议息", "議息", "降息", "加息", "央行利率"):
		return "议息"
	case contains("cpi", "payroll", "inflation", "jobs report", "gdp", "pmi", "非农", "非農", "通胀", "通脹", "就业", "就業"):
		return "宏观"
	case equity:
		return "公司"
	default:
		return "市场"
	}
}

func recentNews(items []NewsItem, now time.Time, equity bool) []NewsItem {
	slices.SortStableFunc(items, func(a, b NewsItem) int { return b.Published.Compare(a.Published) })
	seenTitle, seenURL := make(map[string]bool), make(map[string]bool)
	var result []NewsItem
	for _, n := range items {
		n.Title, n.Source = compactFeedText(n.Title, 280), compactFeedText(n.Source, 60)
		if n.Title == "" || n.Source == "" || n.URL == "" || n.Published.Before(now.AddDate(0, 0, -7)) || n.Published.After(now.Add(5*time.Minute)) {
			continue
		}
		u, _ := url.Parse(n.URL)
		q := u.Query()
		for k := range q {
			if strings.HasPrefix(k, "utm_") || k == ".tsrc" {
				q.Del(k)
			}
		}
		u.RawQuery, u.Fragment = q.Encode(), ""
		key, title := u.String(), strings.ToLower(n.Title)
		if seenTitle[title] || seenURL[key] {
			continue
		}
		n.Category = newsCategory(n.Title, equity)
		seenTitle[title], seenURL[key] = true, true
		result = append(result, n)
		if len(result) == 5 {
			break
		}
	}
	return result
}

func parseRSSNews(body []byte, now time.Time, equity bool) ([]NewsItem, bool) {
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Items []struct {
				Title     string `xml:"title"`
				Link      string `xml:"link"`
				Date      string `xml:"pubDate"`
				Source    string `xml:"Source"`
				Publisher string `xml:"source"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(body, &feed) != nil {
		return nil, false
	}
	var items []NewsItem
	for _, row := range feed.Channel.Items {
		at, err := time.Parse(time.RFC1123Z, strings.TrimSpace(row.Date))
		if err != nil {
			at, err = time.Parse(time.RFC1123, strings.TrimSpace(row.Date))
		}
		if err != nil {
			continue
		}
		source := row.Source
		if source == "" {
			source = row.Publisher
		}
		items = append(items, NewsItem{Title: row.Title, Source: source, URL: rssArticleURL(row.Link), Published: at.UTC()})
	}
	return recentNews(items, now, equity), true
}

func parseYahooNews(body []byte, symbol, company string, now time.Time) ([]NewsItem, bool) {
	var feed struct {
		News []struct {
			Title, Publisher, Link string
			ProviderPublishTime    int64
			RelatedTickers         []string
		}
	}
	if json.Unmarshal(body, &feed) != nil || feed.News == nil {
		return nil, false
	}
	var items []NewsItem
	for _, row := range feed.News {
		title := strings.ToLower(row.Title)
		category := newsCategory(title, true)
		if !slices.Contains(row.RelatedTickers, symbol) ||
			(category != "财报" && category != "时政" && !strings.Contains(title, strings.ToLower(symbol)) &&
				(company == "" || !strings.Contains(title, strings.ToLower(company)))) {
			continue
		}
		items = append(items, NewsItem{Title: row.Title, Source: row.Publisher, URL: SecureNewsURL(row.Link),
			Published: time.Unix(row.ProviderPublishTime, 0).UTC()})
	}
	return recentNews(items, now, true), true
}

func newsTerms(symbol, name, quoteType, language string) (string, string) {
	en := language == "en"
	if quoteType == "EQUITY" {
		name = compactFeedText(name, 50)
		if name == "" {
			name = symbol
		}
		name = strings.ReplaceAll(name, "\"", "")
		if en {
			return "\"" + name + "\" (earnings OR guidance OR management OR regulation OR \"export controls\" OR tariffs)", name
		}
		return "\"" + name + "\" (财报 OR 业绩 OR 管理层 OR 监管 OR 出口管制 OR 关税)", name
	}
	switch symbol {
	case "GC=F":
		if en {
			return "\"gold prices\" OR \"central bank gold\" OR \"safe haven\" OR \"Fed rate\"", "gold prices"
		}
		return "黄金价格 OR 央行购金 OR 美联储 OR 地缘冲突", "黄金价格"
	case "BZ=F":
		if en {
			return "\"Brent crude\" OR \"OPEC+\" OR \"crude inventories\" OR \"Strait of Hormuz\"", "Brent crude"
		}
		return "布伦特原油 OR 欧佩克 OR 原油库存 OR 霍尔木兹海峡", "布伦特原油"
	}
	switch AssetRegion(symbol) {
	case "CN":
		if en {
			return "\"China stocks\" OR \"Hong Kong stocks\" OR \"China PMI\" OR \"China tariff\"", "China stocks"
		}
		return "A股宏观 OR 港股 OR 中国PMI OR 关税政策", "A股宏观"
	case "EU":
		if en {
			return "ECB OR \"Eurozone inflation\" OR \"Europe tariffs\"", "ECB"
		}
		return "欧洲央行 OR 欧元区通胀 OR 欧洲关税", "欧洲央行"
	case "JP":
		if en {
			return "\"Bank of Japan\" OR \"Japan inflation\" OR \"Japan tariffs\"", "Bank of Japan"
		}
		return "日本央行 OR 日本通胀 OR 日本关税", "日本央行"
	case "KR":
		if en {
			return "\"Bank of Korea\" OR \"South Korea inflation\" OR \"South Korea tariffs\"", "Bank of Korea"
		}
		return "韩国央行 OR 韩国通胀 OR 韩国关税", "韩国央行"
	}
	if en {
		return "\"Federal Reserve\" OR \"nonfarm payrolls\" OR \"US inflation\" OR tariffs", "Federal Reserve"
	}
	return "美联储 OR 非农 OR 美国通胀 OR 美国关税", "美联储"
}

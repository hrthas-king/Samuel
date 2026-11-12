package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

var englishNames = map[string]string{"000001.SS": "Shanghai", "399006.SZ": "ChiNext", "399001.SZ": "Shenzhen", "^HSI": "HSI", "^HSTECH": "HSTECH", "^N225": "Nikkei 225", "^KS11": "KOSPI", "^FCHI": "CAC 40", "^GDAXI": "DAX", "^NDX": "Nasdaq 100", "^GSPC": "S&P 500", "GC=F": "Gold", "BZ=F": "Brent", "159632.SZ": "Nasdaq ETF", "159655.SZ": "S&P 500 ETF"}

func groupLabel(language string, group market.GroupSpec) string {
	if locale.Normalize(language) != locale.English {
		return group.MenuLabel
	}
	pair := strings.Join(group.Symbols, " / ")
	switch group.ID {
	case market.GroupVOOQQQ:
		return "S&P 500 / Nasdaq ETFs (" + pair + ")"
	case market.GroupSOXXSMH:
		return "Semiconductor ETFs (" + pair + ")"
	case market.GroupTop2US:
		if len(group.Symbols) == 0 {
			return "Largest U.S. companies · " + locale.Text(language, "等待市值排名")
		}
		return "Largest U.S. companies (" + pair + ")"
	case market.GroupGoldOil:
		return "Gold / Brent crude (GC / BZ)"
	case market.GroupCNIndices:
		return "China indices (Shanghai / ChiNext)"
	case market.GroupHKIndices:
		return "Hong Kong indices (HSI / HSTECH)"
	case market.GroupAsiaIndices:
		return "Japan / Korea (Tencent · Nikkei 225 / KOSPI)"
	case market.GroupEUIndices:
		return "European indices (Yahoo · CAC 40 / DAX)"
	case market.GroupETF159632159655:
		return "Huaan Nasdaq ETF / ChinaAMC S&P 500 ETF"
	case market.GroupUSIndices:
		return "U.S. indices (Nasdaq 100 / S&P 500)"
	case market.GroupCNComposite:
		return "China indices (Shanghai / Shenzhen Component)"
	default:
		return group.MenuLabel
	}
}

func quoteName(language string, view market.View) string {
	if locale.Normalize(language) == locale.English {
		if name := englishNames[view.Symbol]; name != "" {
			return name
		}
	}
	return view.DisplayName()
}

func QuoteSummaryLanguage(s app.Snapshot, language string) string {
	if locale.Normalize(language) != locale.English {
		return QuoteSummary(s)
	}
	var parts []string
	views := s.Quotes
	if len(views) == 0 {
		group := market.GroupByID(s.MarketGroup)
		if group.ID == market.GroupTop2US && len(group.Symbols) == 0 {
			return locale.Text(language, "等待市值排名")
		}
		for _, symbol := range group.Symbols {
			views = append(views, market.View{Symbol: symbol})
		}
	}
	for _, view := range views {
		text := quoteName(language, view) + " --"
		if view.Quote != nil {
			text = fmt.Sprintf("%s %s", quoteName(language, view), locale.Text(language, quotePriceChangeText(*view.Quote)))
			if !view.Healthy {
				text += "!"
			}
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " | ")
}

func OverlayTooltipLanguage(s app.Snapshot, language string) string {
	return snapshotText(language, OverlayTooltip(s), &s)
}

func DiagnosticsTextLanguage(version string, s app.Snapshot, language string) string {
	text := DiagnosticsText(version, s)
	g := market.GroupByID(s.MarketGroup)
	text = strings.ReplaceAll(text, g.MenuLabel, groupLabel(language, g))
	return snapshotText(language, text, &s)
}

// Provider/company and user-defined adapter names are data, not UI captions.
func snapshotText(language, text string, snapshot *app.Snapshot) string {
	if locale.Normalize(language) != locale.English || snapshot == nil {
		return locale.Text(language, text)
	}
	names := []string{snapshot.NIC}
	for _, view := range snapshot.Quotes {
		if englishNames[view.Symbol] == "" {
			names = append(names, view.DisplayName())
			if view.Quote != nil {
				names = append(names, view.Quote.Description)
			}
		}
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	var protected []string
	seen := make(map[string]bool)
	for _, name := range names {
		chinese := strings.IndexFunc(name, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
		if !chinese || seen[name] {
			continue
		}
		seen[name] = true
		token := fmt.Sprintf("\x1fNAME%d\x1f", len(protected))
		text = strings.ReplaceAll(text, name, token)
		protected = append(protected, name)
	}
	text = locale.Text(language, text)
	for i, name := range protected {
		text = strings.ReplaceAll(text, fmt.Sprintf("\x1fNAME%d\x1f", i), name)
	}
	return text
}

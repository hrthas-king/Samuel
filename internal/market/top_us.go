package market

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

var topUSCompanies [2]string // Protected by groupMu.
var usISIN = regexp.MustCompile(`^US[A-Z0-9]{9}[0-9]$`)

// SetTopUSCompanies publishes a complete, ordered pair. The previous pair
// stays in use when a refresh fails to produce two distinct symbols.
func SetTopUSCompanies(symbols [2]string) bool {
	if !watchlist.IsUSSymbol(symbols[0]) || !watchlist.IsUSSymbol(symbols[1]) || symbols[0] == symbols[1] {
		return false
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	if topUSCompanies == symbols {
		return false
	}
	topUSCompanies = symbols
	if activeGroup.ID == GroupTop2US {
		activeGroup = groupByIDLocked(GroupTop2US)
		Symbols = append([]string(nil), activeGroup.Symbols...)
	}
	return true
}

func parseTopUSCompanies(body []byte) ([2]string, error) {
	var response struct {
		Data []struct {
			Columns []json.RawMessage `json:"d"`
		} `json:"data"`
	}
	var leaders [2]string
	if err := json.Unmarshal(body, &response); err != nil {
		return leaders, fmt.Errorf("解析美股市值排名: %w", err)
	}
	type company struct {
		symbol, isin string
		cap          float64
	}
	var candidates []company
	for _, row := range response.Data {
		var item company
		var country, assetType, subtype, currency string
		var primary bool
		columns := []any{&item.symbol, &item.cap, &item.isin, &country, &assetType, &subtype, &currency, &primary}
		if len(row.Columns) != len(columns) {
			return leaders, fmt.Errorf("美股市值排名字段不完整")
		}
		for i, dest := range columns {
			if err := json.Unmarshal(row.Columns[i], dest); err != nil {
				return leaders, fmt.Errorf("美股市值排名字段无效: %w", err)
			}
		}
		if country == "United States" && assetType == "stock" && subtype == "common" &&
			currency == "USD" && primary && positiveFinite(item.cap) {
			candidates = append(candidates, item)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].cap == candidates[j].cap {
			return candidates[i].symbol < candidates[j].symbol
		}
		return candidates[i].cap > candidates[j].cap
	})

	firstIssuer := ""
	for _, item := range candidates {
		symbol := strings.ReplaceAll(item.symbol, ".", "-")
		if !watchlist.IsUSSymbol(symbol) || !usISIN.MatchString(item.isin) {
			return [2]string{}, fmt.Errorf("无法核实美股排名标的的代码或发行人")
		}
		// A U.S. ISIN contains the CUSIP after its country code. The first
		// six CUSIP characters identify the issuer across its share classes.
		issuer := item.isin[:8]
		if firstIssuer == "" {
			firstIssuer, leaders[0] = issuer, symbol
		} else if issuer != firstIssuer && symbol != leaders[0] {
			leaders[1] = symbol
			return leaders, nil
		}
	}
	return [2]string{}, fmt.Errorf("美股市值排名未返回两家可核实的公司")
}

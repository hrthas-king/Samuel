//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	cnnURL        = "https://production.dataviz.cnn.io/index/fearandgreed/graphdata"
	cnnArchiveURL = "https://raw.githubusercontent.com/whit3rabbit/fear-greed-data/main/json/cnn_output.json"
	cnnSource     = "CNN Business"
	cnnArchive    = "CNN Business (public archive)"
)

// The report exposes the original units. Only the scoring engine transforms
// the VIX percentile into a temperature-oriented score.
type FearReading struct {
	Metric
	Source      string     `json:"source,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Rating      string     `json:"rating,omitempty"`
	RatingZH    string     `json:"rating_zh,omitempty"`
}

type FearIndices struct {
	VIX FearReading `json:"vix"`
	CNN FearReading `json:"cnn_fear_greed"`
}

type cnnSnapshot struct {
	Score     *float64  `json:"score"`
	Rating    string    `json:"rating,omitempty"`
	Published time.Time `json:"published_at"`
	History   Series    `json:"history"`
	Source    string    `json:"source"`
	Fetched   time.Time `json:"fetched_at"`
}

func validCNNScore(v *float64) bool { return v != nil && *v >= 0 && *v <= 100 }

func cnnRatingLabel(rating string) string {
	switch rating {
	case "extreme fear":
		return "极度恐惧"
	case "fear":
		return "恐惧"
	case "neutral":
		return "中性"
	case "greed":
		return "贪婪"
	case "extreme greed":
		return "极度贪婪"
	default:
		return ""
	}
}

func parseCNN(body []byte, now time.Time) (cnnSnapshot, error) {
	var feed struct {
		Current struct {
			Score     *float64
			Rating    string
			Timestamp time.Time
		} `json:"fear_and_greed"`
		Historical struct {
			Data []struct{ X, Y json.RawMessage }
		} `json:"fear_and_greed_historical"`
	}
	if err := json.Unmarshal(body, &feed); err != nil {
		return cnnSnapshot{}, errors.New("CNN 指数响应格式无效")
	}
	c := feed.Current
	if !validCNNScore(c.Score) || c.Timestamp.IsZero() || c.Timestamp.After(now) {
		return cnnSnapshot{}, errors.New("CNN 指数读数或原始发布时间无效")
	}
	s := cnnSnapshot{Score: c.Score, Published: c.Timestamp.UTC(), History: Series{}}
	if rating := strings.ToLower(strings.TrimSpace(c.Rating)); cnnRatingLabel(rating) != "" {
		s.Rating = rating
	}
	start := now.AddDate(0, 0, -400).UnixMilli()
	seen := map[string]int64{}
	for _, p := range feed.Historical.Data {
		stamp, err := strconv.ParseFloat(string(p.X), 64)
		// CNN publishes integral epoch milliseconds, including JSON numbers
		// spelled with a trailing ".0".
		if err != nil || stamp != math.Trunc(stamp) || stamp < float64(start) || stamp > float64(c.Timestamp.UnixMilli()) {
			continue
		}
		ms := int64(stamp)
		value, err := strconv.ParseFloat(string(p.Y), 64)
		if err != nil || !validCNNScore(&value) {
			continue
		}
		t := time.UnixMilli(ms).UTC()
		day := nyDate(t)
		// Midnight UTC is CNN's date label, not a trade on the prior US day.
		if ms%(24*time.Hour).Milliseconds() == 0 {
			day = t.Format(dayFormat)
		}
		if previous, ok := seen[day]; ok && ms <= previous {
			if ms == previous && s.History[day] != nil && *s.History[day] != value {
				s.History[day] = nil
			}
			continue
		}
		seen[day], s.History[day] = ms, number(value)
	}
	return s, nil
}

func (s cnnSnapshot) reading(now time.Time, sessions []string, earliest string) (FearReading, error) {
	day := nyDate(s.Published)
	if !validCNNScore(s.Score) || s.Published.IsZero() || s.Published.After(now) {
		return FearReading{}, errors.New("读数或发布时间无效")
	}
	if day < earliest {
		return FearReading{}, fmt.Errorf("最新读数日期 %s，所需日期为 %s 或以后", day, earliest)
	}
	if s.Source == cnnArchive {
		h := s.History[day]
		if !validCNNScore(h) || math.Abs(*h-*s.Score) > 1e-6 {
			return FearReading{}, errors.New("存档头部与同日最后一条历史读数无法核对")
		}
	}
	m := Metric{Date: day, Level: s.Score}
	// An intraday headline may already be available before the equity indices
	// settle. Keep it separate from the preceding close in the lookback.
	if len(sessions) > 0 {
		end := sessions[len(sessions)-1]
		weekday := s.Published.In(ny).Weekday()
		if day > end && end >= nyDate(s.Published.In(ny).AddDate(0, 0, -4)) && weekday != time.Saturday && weekday != time.Sunday {
			sessions = append(sessions, day)
		}
		if sessions[len(sessions)-1] == day {
			v := aligned(s.History, sessions)
			v[len(v)-1] = s.Score
			m = profile(sessions, v)
		}
	}
	return FearReading{Metric: m, Source: s.Source, PublishedAt: &s.Published, Rating: s.Rating, RatingZH: cnnRatingLabel(s.Rating)}, nil
}

func fearDateFloor(now time.Time, sessions []string) string {
	// Also bound the age when equity quotes themselves are missing. Four
	// calendar days allow the previous close across a US holiday weekend.
	earliest := nyDate(now.In(ny).AddDate(0, 0, -4))
	if n := len(sessions); n > 0 {
		earliest = max(earliest, sessions[n-1])
	}
	return earliest
}

// A lagging VIX close cannot confirm a current reversal. The term structure
// may lag independently if the three-month leg hasn't reported yet.
func (m *MarketData) discardStaleVIX(earliest string) []string {
	var issues []string
	for _, key := range []string{"vix", "vix_vix3m_ratio"} {
		metric := m.Measures[key]
		if metric.Date == "" || metric.Date >= earliest {
			continue
		}
		issues = append(issues, fmt.Sprintf("%s 最近观测为 %s，早于 %s，已排除", key, metric.Date, earliest))
		m.Measures[key] = Metric{}
		if key == "vix" {
			m.Measures["vix_1y_pct"], m.Measures["vix_chg_5d_pct"] = Metric{}, Metric{}
			m.VIXDeclining = nil
		}
	}
	return issues
}

func (f Fetcher) cnnFearGreed(ctx context.Context, sessions []string, earliest string) (FearReading, string) {
	path := filepath.Join(f.Dir, "cache", "cnn-fear-greed.json")
	if !f.Force {
		var c cnnSnapshot
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &c) == nil &&
			(c.Source == cnnSource || c.Source == cnnArchive) && !c.Fetched.After(f.Now) && f.Now.Sub(c.Fetched) < 15*time.Minute {
			if r, err := c.reading(f.Now, sessions, earliest); err == nil {
				return r, ""
			}
		}
	}
	var issues []string
	for _, endpoint := range []struct{ url, name string }{{cnnURL, cnnSource}, {cnnArchiveURL, cnnArchive}} {
		body, err := f.get(ctx, endpoint.url, 6<<20)
		var s cnnSnapshot
		if err == nil {
			s, err = parseCNN(body, f.Now)
		}
		if err == nil {
			s.Source, s.Fetched = endpoint.name, f.Now
			var r FearReading
			if r, err = s.reading(f.Now, sessions, earliest); err == nil {
				if err = atomicJSON(path, s); err != nil {
					return r, "CNN 恐惧与贪婪指数缓存写入失败：" + err.Error()
				}
				return r, ""
			}
		}
		issues = append(issues, endpoint.name+" · "+err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return FearReading{}, "CNN 恐惧与贪婪指数缺失：" + strings.Join(issues, "；")
}

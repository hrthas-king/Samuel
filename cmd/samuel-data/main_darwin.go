// SamuelData supplies the native macOS app with quote and chart updates over
// stdin/stdout. It exits when the app closes its input pipe.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/network"
	"github.com/hrthas-king/Samuel/internal/ui"
	"github.com/hrthas-king/Samuel/internal/watchlist"
)

var version = "1.2.5"

type command struct {
	Action string `json:"action"`
	Value  string `json:"value"`
	Second string `json:"second"`
	Number int    `json:"number"`
	On     bool   `json:"on"`
}

type group struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Custom bool   `json:"custom"`
}

type quote struct {
	Symbol   string   `json:"symbol"`
	Name     string   `json:"name"`
	Ticker   string   `json:"ticker"`
	Session  string   `json:"session"`
	Badge    string   `json:"badge"`
	Time     string   `json:"time"`
	Price    string   `json:"price"`
	Currency string   `json:"currency"`
	Move     string   `json:"move"`
	Percent  *float64 `json:"percent,omitempty"`
	Healthy  bool     `json:"healthy"`
	Source   string   `json:"source"`
	Notice   string   `json:"notice"`
}

type chart struct {
	Symbol  string                 `json:"symbol"`
	Points  []market.IntradayPoint `json:"points"`
	Offset  int                    `json:"offset"`
	Base    float64                `json:"base"`
	Digits  int                    `json:"digits"`
	Loading bool                   `json:"loading"`
	Sampled bool                   `json:"sampled"`
	Detail  ui.DetailPresentation  `json:"detail"`
}

type frame struct {
	Language   string  `json:"language"`
	Interval   int     `json:"interval"`
	UpIsRed    bool    `json:"upIsRed"`
	ShowNet    bool    `json:"showNet"`
	Status     string  `json:"status"`
	Country    string  `json:"country"`
	Latency    string  `json:"latency"`
	Up         string  `json:"up"`
	Down       string  `json:"down"`
	Group      string  `json:"group"`
	Groups     []group `json:"groups"`
	Quotes     []quote `json:"quotes"`
	Chart      *chart  `json:"chart,omitempty"`
	Notice     string  `json:"notice"`
	Validating bool    `json:"validating"`
}

type activeChart struct {
	symbol   string
	query    market.IntradayQuery
	remote   bool
	provider string
	points   []market.IntradayPoint
	loading  bool
	samples  market.IntradaySamples
	detail   market.DetailData
}

func (c *activeChart) update(v market.View, lang string, now time.Time, start bool, intraday *market.IntradayClient, detail *market.DetailClient) *chart {
	out := &chart{Symbol: c.symbol, Digits: market.PriceDecimals(v.Symbol), Points: []market.IntradayPoint{}}
	if q := v.Quote; q != nil {
		provider := q.QuoteSource + "/" + q.QuoteType
		start = start || provider != c.provider
		c.provider = provider
		out.Base = q.Previous()
		_, out.Offset = market.QuoteMarketTime(*q).Zone()
		query, remote := market.IntradayFor(*q)
		if remote {
			if start || !c.remote || query != c.query {
				c.points, c.loading = intraday.Request(query)
			} else if c.loading {
				c.points, c.loading = intraday.Result(query)
			}
			out.Points = append(out.Points, c.points...)
			out.Loading = c.loading
		} else {
			if c.remote {
				c.samples = market.IntradaySamples{}
			}
			out.Points = append(out.Points, c.samples.Add(v, now)...)
			out.Sampled = len(out.Points) >= 2
		}
		if !start {
			c.detail = detail.ReadRegularHistory(*q, true, c.detail)
		}
		c.query, c.remote = query, remote
	}
	c.detail = detail.Read(v, ui.QuoteName(lang, v), lang, start, c.detail)
	out.Detail = ui.PresentDetail(v, c.detail, lang, now)
	return out
}

func views(s app.Snapshot, groupID string) []market.View {
	if s.MarketGroup == groupID && len(s.Quotes) > 0 {
		return s.Quotes
	}
	group := market.GroupByID(groupID)
	list := make([]market.View, 0, len(group.Symbols))
	for _, symbol := range group.Symbols {
		list = append(list, market.View{Symbol: symbol})
	}
	return list
}

func present(s app.Snapshot, c config.Config) frame {
	f := frame{
		Language: c.Language, Interval: c.IntervalSeconds, UpIsRed: c.UpIsRed, ShowNet: c.ShowNetDetail,
		Status: string(s.Status), Country: ui.Country(s), Latency: ui.CompactRTT(s), Up: "–", Down: "–", Group: c.MarketGroup,
		Groups: make([]group, 0, len(market.Groups)+len(c.CustomPairs)), Quotes: []quote{},
	}
	if s.Rates.Valid {
		f.Up, f.Down = ui.CompactSpeed(s.Rates.Up), ui.CompactSpeed(s.Rates.Down)
	}
	for _, g := range market.Groups {
		f.Groups = append(f.Groups, group{g.ID, ui.GroupLabel(c.Language, market.GroupByID(g.ID)), false})
	}
	for _, pair := range c.CustomPairs {
		f.Groups = append(f.Groups, group{pair.ID(), pair.Label(), true})
	}
	for _, view := range views(s, c.MarketGroup) {
		q := quote{
			Symbol: view.Symbol, Name: ui.QuoteName(c.Language, view), Ticker: market.TickerFor(view.Symbol),
			Session: ui.SessionName(c.Language, view), Badge: view.EffectiveState().Badge(), Time: ui.QuoteTime(c.Language, view.Quote),
			Price: "–", Healthy: view.Healthy, Notice: locale.Text(c.Language, view.Reason),
		}
		if value := view.Quote; value != nil {
			q.Price = fmt.Sprintf("%.*f", market.PriceDecimals(view.Symbol), value.Price)
			q.Currency = value.Currency
			q.Move = fmt.Sprintf("%+.*f", market.PriceDecimals(view.Symbol), value.ChangeAbs)
			q.Percent = &value.ChangePercent
			q.Source = value.QuoteSource
			if tag := value.FeedTag(time.Now()); tag != "" {
				q.Source += " · " + locale.Text(c.Language, tag)
			}
		}
		f.Quotes = append(f.Quotes, q)
	}
	return f
}

func offer[T any](ch chan T, value T) {
	select {
	case ch <- value:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	ch <- value
}

func main() {
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("SamuelData", version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Load()
	if err := market.SetCustomPairs(cfg.CustomPairs); err != nil {
		return err
	}
	market.SetActiveGroup(cfg.MarketGroup)
	engine := app.New(app.Dependencies{
		ReadCounters: network.ReadCounters, Ping: network.Ping, MeasureLatency: network.MeasureLatency,
		FetchGeo: ipinfo.Fetch, FetchQuotes: market.Fetch, FetchTopUSCompanies: market.FetchTopUSCompanies,
	})
	snapshots := make(chan app.Snapshot, 1)
	go engine.Run(ctx, cfg.IntervalSeconds, func(s app.Snapshot) { offer(snapshots, s) })
	commands := make(chan command)
	go func() {
		defer close(commands)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var cmd command
			if json.Unmarshal(scanner.Bytes(), &cmd) == nil {
				select {
				case commands <- cmd:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	changes := make(chan struct{}, 1)
	// Several detail workers can notify concurrently.
	notify := func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	var intraday *market.IntradayClient
	var detail *market.DetailClient
	var current activeChart
	var snapshot app.Snapshot
	var notice string
	type pairResult struct {
		pair watchlist.Pair
		err  error
	}
	validated := make(chan pairResult, 1)
	validating := false
	encoder := json.NewEncoder(os.Stdout)
	save := func(next config.Config) {
		if err := config.Save(next); err != nil {
			notice = err.Error()
			return
		}
		if err := market.SetCustomPairs(next.CustomPairs); err != nil {
			notice = err.Error()
			return
		}
		if next.MarketGroup != cfg.MarketGroup {
			current = activeChart{}
			offer(engine.Group, next.MarketGroup)
		}
		if next.IntervalSeconds != cfg.IntervalSeconds {
			offer(engine.Interval, next.IntervalSeconds)
		}
		cfg = next
	}
	if err := encoder.Encode(present(snapshot, cfg)); err != nil {
		return err
	}
	for {
		start := false
		select {
		case s := <-snapshots:
			snapshot = s
		case <-changes:
		case result := <-validated:
			validating = false
			if result.err != nil {
				notice = locale.Text(cfg.Language, result.err.Error())
			} else if next, err := cfg.AddPair(result.pair); err == nil {
				save(next)
			}
		case cmd, open := <-commands:
			if !open {
				return nil
			}
			next := cfg
			switch cmd.Action {
			case "open":
				for _, view := range views(snapshot, cfg.MarketGroup) {
					if view.Symbol == cmd.Value {
						if intraday == nil {
							intraday, detail = market.NewIntradayClient(ctx, notify), market.NewDetailClient(ctx, notify)
						}
						if current.symbol != cmd.Value {
							current = activeChart{symbol: cmd.Value}
						}
						start = true
						break
					}
				}
			case "close":
				current = activeChart{}
			case "refresh":
				offer(engine.Refresh, struct{}{})
				if cfg.MarketGroup == market.GroupTop2US {
					engine.RequestTopUSRefresh()
				}
				start = true
			case "group":
				if cfg.HasMarketGroup(cmd.Value) {
					next.MarketGroup = cmd.Value
					save(next)
				}
			case "language":
				if locale.Valid(cmd.Value) {
					next.Language = cmd.Value
					save(next)
					current.detail.News = market.DetailResult[[]market.NewsItem]{}
					start = true
				}
			case "network":
				next.ShowNetDetail = cmd.On
				save(next)
			case "color":
				next.UpIsRed = cmd.On
				save(next)
			case "interval":
				if config.ValidInterval(cmd.Number) {
					next.IntervalSeconds = cmd.Number
					save(next)
				}
			case "add":
				if !validating {
					notice, validating = "", true
					go func() {
						pair, err := market.ValidateCustomPair(ctx, cmd.Value, cmd.Second)
						validated <- pairResult{pair, err}
					}()
				}
			case "remove":
				if next, err := cfg.RemovePair(cmd.Value); err == nil {
					save(next)
				}
			}
		}
		f := present(snapshot, cfg)
		f.Notice, f.Validating = notice, validating
		for _, view := range views(snapshot, cfg.MarketGroup) {
			if view.Symbol == current.symbol {
				f.Chart = current.update(view, cfg.Language, time.Now(), start, intraday, detail)
				break
			}
		}
		if err := encoder.Encode(f); err != nil {
			return err
		}
	}
}

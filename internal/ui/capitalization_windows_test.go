//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

func TestStockCapitalizationNativeRowsAndAssetFiltering(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	stock := market.Quote{Symbol: "NVDA", Alias: "NVDA", QuoteType: "EQUITY", Price: 210.15, ChangePercent: -1.64, State: market.StatePostMarket,
		Capitalization: &market.Capitalization{USD: 5.07e12, Estimated: true}}
	apple := stock
	apple.Symbol, apple.Alias, apple.Price, apple.ChangePercent = "AAPL", "Apple Inc.", 352.05, .90
	apple.Capitalization = &market.Capitalization{USD: 5.14e12, Estimated: true}
	etf := market.Quote{Symbol: "VOO", Alias: "VOO", QuoteType: "ETF", Price: 702.97, ChangePercent: -.62, State: market.StatePostMarket,
		Capitalization: &market.Capitalization{USD: 1e12}} // must not be shown for an ETF
	for _, kind := range []string{"stocks", "mixed", "etfs", "indices", "missing_indices", "index_etf"} {
		for _, pct := range []int{75, 100, 130} {
			for _, dpi := range []float64{1, 2} {
				t.Run(fmt.Sprintf("%s/%d/%g", kind, pct, dpi), func(t *testing.T) {
					cfg := config.Default()
					cfg.UIScalePercent = pct
					u := NewUI("test", cfg)
					u.dpiScale = dpi
					u.recalcScale()
					u.updateDimensions()
					u.rebuildFonts()
					u.bgBrush, _, _ = createSolidBrush.Call(colorRef(1, 1, 1))
					u.brushGreen, _, _ = createSolidBrush.Call(colorRef(0, 230, 118))
					u.brushBlue, _, _ = createSolidBrush.Call(colorRef(56, 189, 248))
					u.brushGray, _, _ = createSolidBrush.Call(colorRef(148, 163, 184))
					defer func() {
						for _, object := range []uintptr{u.font, u.stockFont, u.capFont, u.bgBrush, u.brushGreen, u.brushBlue, u.brushGray} {
							deleteObject.Call(object)
						}
					}()
					first, second := stock, apple
					if kind == "mixed" {
						second = etf
					} else if kind == "etfs" {
						first, second = etf, etf
					} else if kind == "indices" || kind == "missing_indices" || kind == "index_etf" {
						pePct, pbPct := 23.5, 60.0
						first = market.Quote{Symbol: "^NDX", Alias: "纳指100", QuoteType: "INDEX", Price: 28384.85, ChangePercent: -3.46, State: market.StateClosed,
							Valuation: &market.IndexValuation{PE: &market.ValuationMetric{Value: 27.04, Percentile: &pePct}, PB: &market.ValuationMetric{Value: 8.65, Percentile: &pbPct}}}
						second = first
						second.Symbol, second.Alias, second.Price = "^GSPC", "标普500", 7580.07
						second.ChangePercent = -1.82
						spPE, spPB := 52.7, 92.3
						second.Valuation = &market.IndexValuation{PE: &market.ValuationMetric{Value: 24.86, Percentile: &spPE}, PB: &market.ValuationMetric{Value: 5.44, Percentile: &spPB}}
						if kind == "missing_indices" {
							first.Valuation, second.Valuation = nil, nil
						} else if kind == "index_etf" {
							second = etf
						}
					}
					snap := app.Snapshot{Status: monitor.Online, Geo: ipinfo.Info{Country: "US"}, Quotes: []market.View{
						{Symbol: first.Symbol, Quote: &first, Healthy: true}, {Symbol: second.Symbol, Quote: &second, Healthy: true},
					}}
					u.latest.Store(&snap)
					// First pass measures the content; second pass renders its exact bounds.
					renderFluentBitmap(t, int(u.px(2000)), int(u.height), func(dc uintptr) { u.drawBar(dc, 0) })
					img := renderFluentBitmap(t, int(u.width), int(u.height), func(dc uintptr) {
						if kind != "etfs" {
							sample := "Ag 市值 $5.07T"
							text := winapi.UTF16(sample)
							for _, row := range []struct {
								font   uintptr
								height int32
							}{{u.stockFont, u.px(17)}, {u.capFont, u.height - u.px(17)}} {
								old, _, _ := selectObject.Call(dc, row.font)
								var size textSize
								getTextExtentPoint32.Call(dc, uintptr(unsafe.Pointer(text)), uintptr(len([]rune(sample))), uintptr(unsafe.Pointer(&size)))
								selectObject.Call(dc, old)
								if size.CY > row.height {
									t.Fatalf("font clipped in capitalization rows: font=%d row=%d", size.CY, row.height)
								}
							}
						}
						u.drawBar(dc, 0)
					})
					capPixels := 0
					for y := 0; y < img.Bounds().Dy(); y++ {
						for x := 0; x < img.Bounds().Dx(); x++ {
							pixel := img.NRGBAAt(x, y)
							if pixel.R == 175 && pixel.G == 185 && pixel.B == 198 {
								capPixels++
								if y < int(u.px(17)) {
									t.Fatal("market cap overlaps the price row")
								}
							}
						}
					}
					if (capPixels > 0) != (kind != "etfs") || u.height != u.px(barHeightLogical) {
						t.Fatalf("wrong asset filtering or taskbar height: pixels=%d height=%d", capPixels, u.height)
					}
					if dir := os.Getenv("SAMUEL_CAP_PREVIEWS"); dir != "" && pct == 100 && dpi == 2 {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(dir, kind+".png"))
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(file, img)
						file.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

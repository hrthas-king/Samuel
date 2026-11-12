//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
)

func pixelHasTint(pixel, tint uint32) bool {
	a := pixel >> 24
	if a < 64 {
		return false
	}
	return overCoverage(0, tint, a) == pixel
}

func dibImage(d *dibSurface, width int32) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(width), int(d.height)))
	for y := range d.height {
		for x := range width {
			c := d.pixels[y*d.width+x]
			i := img.PixOffset(int(x), int(y))
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(c>>16), byte(c>>8), byte(c), byte(c>>24)
		}
	}
	return img
}

func saveChartPreview(t *testing.T, name string, d *dibSurface, width int32) {
	t.Helper()
	dir := os.Getenv("SAMUEL_CHART_PREVIEWS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, dibImage(d, width)); err != nil {
		t.Fatal(err)
	}
}

func barRenderFixture() app.Snapshot {
	first := &market.Quote{Symbol: "159632.SZ", Price: 2.422, Currency: "CNY", State: market.StateOvernight}
	second := &market.Quote{Symbol: "159655.SZ", Price: 1.936, ChangePercent: -3.10, Currency: "CNY", State: market.StatePreMarket}
	return app.Snapshot{Geo: ipinfo.Info{Country: "US"}, Status: monitor.Online,
		Rates:   monitor.Rates{Up: 1740, Down: 2560, Valid: true},
		Latency: monitor.Latency{RTT: 799 * time.Millisecond, At: time.Now(), Method: "https", Target: "test"},
		Quotes:  []market.View{{Symbol: first.Symbol, Quote: first, Healthy: true}, {Symbol: second.Symbol, Quote: second, Healthy: true}}}
}

func TestBarBitmapHasOnlyPremultipliedForeground(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, dpi := range []float64{1, 1.25, 1.5, 2} {
		for _, pct := range []int{75, 100, 130} {
			for _, detail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%g/%d/%t", dpi, pct, detail), func(t *testing.T) {
					cfg := config.Default()
					cfg.Language, cfg.UIScalePercent, cfg.ShowNetDetail = locale.English, pct, detail
					u := NewUI("test", cfg)
					u.dpiScale = dpi
					u.recalcScale()
					u.updateDimensions()
					u.rebuildFonts()
					defer u.releaseFonts()
					defer u.barBitmap.close()
					snap := barRenderFixture()
					u.latest.Store(&snap)
					if err := u.renderBarBitmap(); err != nil {
						t.Fatal(err)
					}
					net, first, second := u.barHits[0].bounds, u.barHits[1].bounds, u.barHits[2].bounds
					if !(net.Right < first.Left && first.Right < second.Left && second.Right < u.width) {
						t.Fatalf("hit regions overlap: %v", u.barHits)
					}
					greenRight, redLeft, moonAA, sunAA, textAA := int32(-1), u.width, 0, 0, 0
					redQuote := false
					for y := int32(0); y < u.height; y++ {
						for x := int32(0); x < u.width; x++ {
							pixel := u.barBitmap.pixels[y*u.barBitmap.width+x]
							a := pixel >> 24
							if (pixel>>16)&255 > a || (pixel>>8)&255 > a || pixel&255 > a {
								t.Fatalf("non-premultiplied pixel (%d,%d) %#08x", x, y, pixel)
							}
							if y == 0 || y == u.height-1 || x < u.px(3) || x >= second.Right {
								if pixel != 0 {
									t.Fatalf("background must be ARGB(0,0,0,0) at (%d,%d), got %#08x", x, y, pixel)
								}
							}
							p := point{x, y}
							if within(net, p) && pixelHasTint(pixel, barUp) {
								greenRight = max(greenRight, x)
							}
							if within(net, p) && pixelHasTint(pixel, barDown) {
								redLeft = min(redLeft, x)
							}
							if a < 255 && within(first, p) && pixelHasTint(pixel, markerColor(market.StateOvernight)) {
								moonAA++
							}
							if a < 255 && within(second, p) && pixelHasTint(pixel, markerColor(market.StatePreMarket)) {
								sunAA++
							}
							if a < 255 && within(first, p) && pixelHasTint(pixel, barWhite) {
								textAA++
							}
							redQuote = redQuote || (within(second, p) && pixelHasTint(pixel, 0xFFFF5C6C))
						}
					}
					if greenRight < 0 || greenRight >= redLeft || redLeft >= net.Right || moonAA == 0 || sunAA == 0 || textAA == 0 || !redQuote {
						t.Fatalf("missing foreground colors or AA: up=%d down=%d moon=%d sun=%d text=%d negative=%v", greenRight, redLeft, moonAA, sunAA, textAA, redQuote)
					}
					if detail && dpi == 1 && pct == 100 {
						saveChartPreview(t, "taskbar", &u.barBitmap.dibSurface, u.width)
					}
				})
			}
		}
	}
}

func chartRenderFixture() (market.View, []market.IntradayPoint) {
	at := time.Date(2026, 11, 6, 0, 59, 0, 0, time.UTC).Unix()
	q := &market.Quote{Symbol: "QQQ", Alias: "Invesco QQQ", Currency: "USD", Price: 647.05, ChangeAbs: -44.90,
		ChangePercent: -6.49, State: market.StatePostMarket, TradeTime: at, ChangeBase: 691.95}
	var points []market.IntradayPoint
	for i, price := range []float64{692, 687, 685, 689, 679, 673, 676, 671, 666, 673, 662, 659, 660, 655, 652, 654, 647.05} {
		points = append(points, market.IntradayPoint{Time: at - 300*int64(16-i), Price: price})
	}
	return market.View{Symbol: q.Symbol, Quote: q, Healthy: true}, points
}

func TestChartBitmapAtDPIAndUIScale(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	view, points := chartRenderFixture()
	for _, dpi := range []float64{1, 1.25, 1.5, 2} {
		for _, pct := range []int{75, 100, 130} {
			for _, language := range []string{locale.Chinese, locale.English} {
				t.Run(fmt.Sprintf("%g/%d/%s", dpi, pct, language), func(t *testing.T) {
					scale := dpi * float64(pct) / 100
					w, h := int32(math.Round(chartWidth*scale)), int32(math.Round(chartHeight*scale))
					var raster chartRaster
					defer raster.close()
					if err := raster.render(w, h, scale, language, false, view, chartSeries{points: points}, nil); err != nil {
						t.Fatal(err)
					}
					pixel := func(x, y int32) uint32 { return raster.pixels[y*w+x] }
					left, right := raster.px(22), w-raster.px(22)
					top, bottom := raster.px(124), h-raster.px(chartCloseRow)-raster.px(34)
					color := changeColor(view.Quote.ChangePercent, false)
					if pixel(0, 0) != chartBackground || pixel(w-1, h-1) != chartBackground {
						t.Fatal("dark card surface did not stay inside its bounds")
					}
					var headerAccent, chartAccent, timePixels int
					for y := range h {
						for x := range w {
							c := pixel(x, y)
							if c>>24 != 255 {
								t.Fatalf("card is not opaque at (%d,%d)", x, y)
							}
							if c == color && y >= raster.px(43) && y < raster.px(93) && x > w/2 {
								headerAccent++
							}
							if c == color && y >= top && y < bottom {
								chartAccent++
							}
							if c != chartBackground && y >= raster.px(96) && y < raster.px(115) {
								timePixels++
							}
						}
					}
					line, _ := chartCoordinates(points, view.Quote.Previous(), float32(left), float32(top), float32(right), float32(bottom))
					dotY := int32(math.Round(float64(line[len(line)-1].Y)))
					if headerAccent == 0 || chartAccent < int(right-left)/2 || timePixels == 0 ||
						pixel(right, dotY) != color || pixel(right+raster.px(6), dotY) == chartBackground {
						t.Fatalf("missing chart components: change=%d line=%d time=%d dot=%x halo=%x",
							headerAccent, chartAccent, timePixels, pixel(right, dotY), pixel(right+raster.px(6), dotY))
					}
					near, far := pixel(left+raster.px(9), top+(bottom-top)/3), pixel(left+raster.px(9), bottom-raster.px(6))
					if byte(near>>16) <= byte(far>>16) || byte(far>>16) < (chartBackground>>16)&255 {
						t.Fatalf("chart shade does not fade vertically: near=%#x far=%#x", near, far)
					}
					if dpi == 1 && pct == 100 {
						saveChartPreview(t, "flyout-"+language, &raster.dibSurface, w)
					}
					for _, loading := range []bool{true, false} {
						if err := raster.render(w, h, scale, language, true, view, chartSeries{loading: loading}, nil); err != nil {
							t.Fatal(err)
						}
						status, greenChange := false, false
						for y := raster.px(43); y < bottom; y++ {
							for x := left; x < right; x++ {
								c := pixel(x, y)
								status = status || (y > top && c != chartBackground)
								greenChange = greenChange || (y < raster.px(93) && c == changeColor(view.Quote.ChangePercent, true))
							}
						}
						if !status || !greenChange {
							t.Fatal("missing placeholder or inverted change color")
						}
					}
				})
			}
		}
	}
}

func TestChartPlacementUsesMonitorWorkArea(t *testing.T) {
	work := rect{-1920, 40, 0, 1040}
	topBar := rect{-1920, 5, -1480, 35}
	bottomBar := rect{-500, 1045, -60, 1075}
	for _, bar := range []rect{topBar, bottomBar} {
		bounds, _ := chartPlacement(bar, work, bar.Left+120, 2)
		if bounds.Left < work.Left || bounds.Right > work.Right || bounds.Top < work.Top || bounds.Bottom > work.Bottom {
			t.Fatalf("chart escaped the work area: %+v", bounds)
		}
		if bar == topBar && bounds.Top < bar.Bottom || bar == bottomBar && bounds.Bottom > bar.Top {
			t.Fatalf("wrong side of the taskbar: bar=%+v chart=%+v", bar, bounds)
		}
	}
	small := rect{0, 0, 420, 290}
	bounds, scale := chartPlacement(rect{0, 295, 410, 340}, small, 5, 2.6)
	if scale >= 2.6 || bounds.Left < 0 || bounds.Right > small.Right || bounds.Top < 0 || bounds.Bottom > small.Bottom {
		t.Fatalf("small-monitor layout did not shrink: %+v scale=%g", bounds, scale)
	}
}

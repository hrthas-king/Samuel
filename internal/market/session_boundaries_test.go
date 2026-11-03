package market

import (
	"strings"
	"testing"
	"time"
)

func TestCashAfternoonNeedsANewSessionQuoteAndHKHasClosingAuction(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	beforeLunch := time.Date(2026, 11, 3, 11, 29, 0, 0, zone).Unix()
	afterLunch := time.Date(2026, 11, 3, 13, 0, 0, 0, zone)
	if state, _ := resolveMainlandSession(afterLunch, beforeLunch); state != StateClosed {
		t.Error("custom A-share morning quote promoted to the afternoon session")
	}
	for _, symbol := range []string{"000001.SS", "159632.SZ", "^HSI"} {
		if state, _ := resolveIndexSession(symbol, afterLunch, beforeLunch); state != StateClosed {
			t.Error(symbol, "morning quote promoted to the afternoon session")
		}
	}
	auction := time.Date(2026, 11, 3, 16, 5, 0, 0, zone)
	state, label := resolveIndexSession("^HSI", auction, auction.Add(-time.Second).Unix())
	if state != StateRegular || !strings.Contains(label, "收市竞价") {
		t.Error("HK closing auction must be explicit rather than an ordinary close:", state, label)
	}
}

func TestGoldWinterMaintenanceUsesNewYorkClock(t *testing.T) {
	state, _ := resolveCommoditySession(time.Date(2026, 11, 3, 22, 30, 0, 0, time.UTC)) // 17:30 EST
	if state != StateClosed {
		t.Fatal("winter 17:00-18:00 New York maintenance was shifted by an hour:", state)
	}
}

func TestGoldAndBrentKeepDifferentMaintenanceHours(t *testing.T) {
	for _, tc := range []struct {
		symbol, at string
		active     bool
	}{
		{"GC=F", "2026-11-03T22:30:00Z", false},
		{"BZ=F", "2026-11-03T22:30:00Z", true},
		{"GC=F", "2026-11-03T23:30:00Z", true},
		{"BZ=F", "2026-11-03T23:30:00Z", false},
		{"BZ=F", "2026-11-04T00:30:00Z", false},
		{"BZ=F", "2026-11-04T01:00:00Z", true},
		{"BZ=F", "2026-11-01T23:00:00Z", true},
		{"BZ=F", "2026-11-02T00:30:00Z", true},
		{"BZ=F", "2026-10-19T22:30:00Z", false},
		{"GC=F", "2026-10-26T21:30:00Z", false},
	} {
		t.Run(tc.symbol+tc.at, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.at)
			_, _, active := commodityWindow(tc.symbol, now)
			if active != tc.active {
				t.Fatal("wrong commodity maintenance or DST boundary:", active)
			}
			state, _ := resolveCommodityQuoteSession(tc.symbol, now, now.Add(-24*time.Hour).Unix())
			if state != StateClosed {
				t.Fatal("prior trading day's quote was promoted into the current session")
			}
		})
	}
}

func TestMainlandAuctionMustNotPromotePriorDayClose(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 11, 3, 9, 20, 0, 0, zone)
	previous := time.Date(2026, 11, 2, 15, 0, 0, 0, zone).Unix()
	state, _ := resolveMainlandSession(now, previous)
	if state != StateClosed {
		t.Error("custom A-share prior close presented as today's auction:", state)
	}
	for _, symbol := range []string{"159632.SZ", "159655.SZ"} {
		state, _ := resolveIndexSession(symbol, now, previous)
		if state != StateClosed {
			t.Error(symbol, "prior close presented as today's auction:", state)
		}
	}
}

func TestCashIndicesStopAtLunchAndClosingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		symbol, at string
	}{
		{"000001.SS", "2026-11-03T03:30:00Z"},
		{"399001.SZ", "2026-11-03T07:00:00Z"},
		{"^N225", "2026-11-02T02:30:00Z"},
		{"^N225", "2026-11-02T06:30:00Z"},
		{"^KS11", "2026-11-03T06:30:00Z"},
		{"^N225", "2026-11-02T23:45:00Z"},
		{"^KS11", "2026-11-02T23:45:00Z"},
	} {
		t.Run(tc.symbol+tc.at, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.at)
			state, _ := resolveIndexSession(tc.symbol, now, now.Add(-time.Minute).Unix())
			if state != StateClosed {
				t.Fatal("cash index advertised a trade outside its cash session:", state)
			}
		})
	}
}

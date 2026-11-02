//go:build windows && (amd64 || arm64)

package ui

import (
	"strings"
	"testing"

	"github.com/hrthas-king/Samuel/internal/market"
)

func TestMarketGroupActionsIncludeETFPairWithoutCollidingWithScale(t *testing.T) {
	if got, want := len(market.Groups), int(ActionScale75-ActionGroupVOOQQQ); got != want {
		t.Fatalf("market groups/actions differ: groups=%d, actions=%d", got, want)
	}
	for i, group := range market.Groups {
		got, ok := GroupForAction(ActionGroupVOOQQQ + Action(i))
		if !ok || got != group.ID {
			t.Errorf("group action %d returned %q, %v; want %q", i, got, ok, group.ID)
		}
	}
	groupID, ok := GroupForAction(ActionGroupETF159632159655)
	if !ok || groupID != "etf_159632_159655" {
		t.Fatalf("ETF action returned %q, %v", groupID, ok)
	}
	label := market.GroupByID(groupID).MenuLabel
	if label != "华安纳指ETF / 华夏标普500ETF" || strings.Contains(label, "159632") || strings.Contains(label, "159655") {
		t.Fatalf("ETF menu label should use names instead of security codes: %q", label)
	}
	if _, ok := GroupForAction(ActionScale75); ok {
		t.Fatal("first size action must not select a market group")
	}
	if scale, ok := ScaleForAction(ActionScale75); !ok || scale != 75 {
		t.Fatalf("first size action returned %d, %v", scale, ok)
	}
	for action, want := range map[Action]string{ActionGroupUSIndices: market.GroupUSIndices, ActionGroupCNComposite: market.GroupCNComposite} {
		if got, ok := GroupForAction(action); !ok || got != want {
			t.Fatalf("new index menu action selected %q instead of %q", got, want)
		}
	}
}

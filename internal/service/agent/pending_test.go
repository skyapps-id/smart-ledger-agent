package agent

import (
	"testing"

	"smart-ledger-agent/internal/domain"
)

func newTestPending() *PendingConfirms {
	return NewPendingConfirms()
}

func TestPendingResolveByNumber(t *testing.T) {
	p := newTestPending()
	p.Set("c1", PendingChoice{
		Action:    domain.ActionConsumption,
		Params:    map[string]interface{}{"consumption_action": "complete", "item_name": "susu bmt 200g"},
		OptionKey: "batch_number",
		Options:   []string{"SEP-03-232248", "SEP-01-151030"},
	})

	action, _, ok := p.Resolve("c1", "2")
	if !ok {
		t.Fatal("expected resolve by number 2")
	}
	if action.Action != domain.ActionConsumption {
		t.Errorf("wrong action: %s", action.Action)
	}
	if action.Params["batch_number"] != "SEP-01-151030" {
		t.Errorf("wrong batch: %v", action.Params["batch_number"])
	}
	if action.Params["consumption_action"] != "complete" {
		t.Errorf("original params lost: %v", action.Params)
	}

	// Pending terkonsumsi — pesan berikutnya diproses normal.
	if _, _, ok := p.Resolve("c1", "1"); ok {
		t.Error("pending should be consumed after resolve")
	}
}

func TestPendingResolveVariants(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"1", "A-1"},
		{" 2 ", "A-2"},
		{"batch 2", "A-2"},
		{"pilih 1", "A-1"},
		{"a-1", "A-1"}, // nilai lengkap, case-insensitive
	}
	for _, tc := range cases {
		p := newTestPending()
		p.Set("c1", PendingChoice{
			Action: "consumption", OptionKey: "batch_number", Options: []string{"A-1", "A-2"},
		})
		action, _, ok := p.Resolve("c1", tc.text)
		if !ok {
			t.Errorf("text %q: expected resolve", tc.text)
			continue
		}
		if action.Params["batch_number"] != tc.want {
			t.Errorf("text %q: expected %s, got %v", tc.text, tc.want, action.Params["batch_number"])
		}
	}
}

func TestPendingCancelOnOtherMessage(t *testing.T) {
	p := newTestPending()
	p.Set("c1", PendingChoice{Action: "consumption", OptionKey: "batch_number", Options: []string{"A", "B"}})

	// Pesan bukan pilihan → pending batal, diproses normal.
	if _, _, ok := p.Resolve("c1", "beli kopi 15rb"); ok {
		t.Error("non-choice message must not resolve")
	}
	if _, _, ok := p.Resolve("c1", "1"); ok {
		t.Error("pending must be cancelled after unrelated message")
	}
}

func TestPendingOutOfRangeAndEmpty(t *testing.T) {
	p := newTestPending()
	p.Set("c1", PendingChoice{OptionKey: "batch_number", Options: []string{"A", "B"}})
	for _, text := range []string{"3", "0", "-1", ""} {
		if _, _, ok := p.Resolve("c1", text); ok {
			t.Errorf("text %q must not resolve", text)
		}
	}
	// Semua di atas membatalkan pending.
	if _, _, ok := p.Resolve("c1", "1"); ok {
		t.Error("pending must be cancelled")
	}
}

func TestPendingIsolatedPerChat(t *testing.T) {
	p := newTestPending()
	p.Set("chat-1", PendingChoice{Action: "consumption", OptionKey: "batch_number", Options: []string{"X1"}})
	p.Set("chat-2", PendingChoice{Action: "consumption", OptionKey: "batch_number", Options: []string{"Y1"}})

	if a, _, _ := p.Resolve("chat-2", "1"); a.Params["batch_number"] != "Y1" {
		t.Errorf("chat-2 must get its own option, got %v", a.Params["batch_number"])
	}
	if _, _, ok := p.Resolve("chat-1", "1"); !ok {
		t.Error("chat-1 pending must still exist")
	}
}

func TestPendingCarriesOriginalText(t *testing.T) {
	p := newTestPending()
	p.Set("c1", PendingChoice{
		Action:       "record_transaction",
		OptionKey:    "item_name",
		Options:      []string{"susu uht 500ml", "susu bmt 200g"},
		OriginalText: "ambil susu 2 pcs",
	})

	action, origText, ok := p.Resolve("c1", "2")
	if !ok || origText != "ambil susu 2 pcs" {
		t.Fatalf("expected original text carried, got %q ok=%v", origText, ok)
	}
	if action.Params["item_name"] != "susu bmt 200g" {
		t.Errorf("wrong item: %v", action.Params["item_name"])
	}
}

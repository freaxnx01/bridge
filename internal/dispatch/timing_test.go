package dispatch

import (
	"testing"
	"time"
)

func TestResolveTiming_MorningHandover_IsGuarded(t *testing.T) {
	cfg := DefaultConfig() // 18:00-07:00 no rung, 07:00-18:00 rung; 5h window
	day := func(h, m int) time.Time { return time.Date(2026, 9, 18, h, m, 0, 0, time.Local) }

	tests := []struct {
		name            string
		now             time.Time
		wantRung        bool
		wantMeasureFrom time.Time
		wantNightCap    bool
	}{
		{
			name: "deep night burns freely", now: day(23, 0),
			wantRung: false, wantNightCap: true,
		},
		{
			// 01:00 + 5h = 06:00, aged out before the handover.
			name: "01:00 is still outside the shoulder", now: day(1, 0),
			wantRung: false, wantNightCap: true,
		},
		{
			// 05:00 + 5h = 10:00: this spend is in the operator's window at
			// 07:00, so it is measured against the 02:00-07:00 span.
			name: "05:00 is guarded for the 07:00 handover", now: day(5, 0),
			wantRung: true, wantMeasureFrom: day(2, 0), wantNightCap: true,
		},
		{
			name: "midday measures the plain trailing window", now: day(12, 0),
			wantRung: true, wantMeasureFrom: day(7, 0), wantNightCap: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTiming(cfg, tc.now)
			if got.RungOn != tc.wantRung {
				t.Fatalf("RungOn=%v want %v", got.RungOn, tc.wantRung)
			}
			if tc.wantRung && !got.MeasureFrom.Equal(tc.wantMeasureFrom) {
				t.Errorf("MeasureFrom=%v want %v", got.MeasureFrom, tc.wantMeasureFrom)
			}
			if got.NightCapApplies != tc.wantNightCap {
				t.Errorf("NightCapApplies=%v want %v", got.NightCapApplies, tc.wantNightCap)
			}
		})
	}
}

func TestNightStartForReport_OutsideAnUnattendedWindow_FallsBackToTheLatestOne(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.Local)
	timing := ResolveTiming(cfg, now)
	if timing.NightCapApplies {
		t.Fatal("midday is not an unattended window")
	}

	got := NightStartForReport(cfg, timing, now)

	want := time.Date(2026, 9, 17, 18, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestRungLabel_EachTimingShape_NamesItsSource(t *testing.T) {
	cfg := DefaultConfig()
	day := func(h int) time.Time { return time.Date(2026, 9, 18, h, 0, 0, 0, time.Local) }

	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"inside a rung window", day(12), "active (window 07:00-18:00)"},
		{"the pre-dawn shoulder", day(5), "active (reserving headroom for the 07:00 handover)"},
		{"inside an unguarded window", day(23), "inactive (window 18:00-07:00)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RungLabel(ResolveTiming(cfg, tc.now)); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

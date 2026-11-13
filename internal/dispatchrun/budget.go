package dispatchrun

import (
	"context"
	"log/slog"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/usage"
)

// MeasureWindowUSD sums interactive and pipeline consumption over the trailing
// quota window. The bool reports whether the number can be trusted: false means
// fail closed, and is never the same as a measured zero.
func MeasureWindowUSD(ctx context.Context, cfg dispatch.Config,
	transcriptRoot, ledgerPath string, from, to time.Time) (float64, bool) {
	turns, err := usage.ScanTranscripts(ctx, transcriptRoot, from)
	if err != nil {
		slog.Warn("dispatch: cannot read Claude transcripts — guarded dispatch will be blocked", "error", err)
		return 0, false
	}
	ledger, err := usage.LoadLedger(ledgerPath)
	if err != nil {
		slog.Warn("dispatch: cannot read the usage ledger — guarded dispatch will be blocked", "error", err)
		return 0, false
	}

	pricing := usage.DefaultPricing().Merge(cfg.Budget.Pricing)
	return usage.SumWindow(turns, pricing, from, to) + ledger.SumSince(from), true
}

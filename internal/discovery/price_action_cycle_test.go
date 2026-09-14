package discovery

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPriceActionCycleRequiresCompleteOHLCWindow(t *testing.T) {
	rows := priceActionTestRows(20, false)
	result := buildPriceActionCycleAnalysis(rows, CandidateRelativeStrength{Status: "missing"})
	if result.Phase != PriceActionPhaseUnavailable || result.Status != PriceActionPhaseUnavailable {
		t.Fatalf("phase = %+v, want unavailable", result)
	}
}

func TestPriceActionCycleDetectsMatureTrendBreakout(t *testing.T) {
	rows := priceActionTestRows(60, true)
	// Keep the breakout constructive rather than extremely extended.
	last := &rows[len(rows)-1]
	last.CloseMicros = rows[len(rows)-2].HighMicros + 120_000
	last.OpenMicros = last.CloseMicros - 80_000
	last.HighMicros = last.CloseMicros + 100_000
	last.LowMicros = last.OpenMicros - 100_000
	last.Volume = 250_000
	result := buildPriceActionCycleAnalysis(rows, CandidateRelativeStrength{Status: "missing"})
	if result.Status != "ready" || (result.Phase != PriceActionPhaseBaseBreak && result.Phase != PriceActionPhaseWedgePop) {
		t.Fatalf("phase = %+v, want confirmed breakout", result)
	}
	if result.Confidence < 50 || len(result.Evidence) < 2 || result.RuleVersion != PriceActionCycleRuleVersion {
		t.Fatalf("explanation = %+v, want versioned evidence", result)
	}
}

func TestPriceActionCycleUsesLatestCompleteOHLCInsteadOfCloseOnlyCacheRow(t *testing.T) {
	rows := priceActionTestRows(60, true)
	completeDate := rows[len(rows)-1].TradeDate.Format(time.DateOnly)
	rows = append(rows, PriceSnapshot{TradeDate: rows[len(rows)-1].TradeDate.AddDate(0, 0, 1), CloseMicros: 14_000_000, QualityStatus: QualityStatusValid})
	result := buildPriceActionCycleAnalysis(rows, CandidateRelativeStrength{Status: "missing"})
	if result.Status != "ready" {
		t.Fatalf("phase = %+v, want classification from complete history", result)
	}
	if result.TradeDate != completeDate {
		t.Fatalf("trade date = %s, want latest complete date %s", result.TradeDate, completeDate)
	}
}

func TestPriceActionSnapshotsAreIdempotentAndCarryPreviousPhase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:price-action?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&PriceSnapshot{}, &Listing{}, &PriceActionPhaseSnapshot{}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := priceActionTestRows(60, true)
	for i := range rows {
		rows[i].Symbol = "CYCLE"
		rows[i].Source = "test"
		rows[i].SourceVersion = "v1"
		rows[i].QualityStatus = QualityStatusValid
		rows[i].TradeDate = base.AddDate(0, 0, i)
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Listing{SecurityID: 7, Ticker: "CYCLE", ValidFrom: base, MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	created, err := RecordPriceActionPhaseSnapshots(context.Background(), db, []string{"cycle"}, base.AddDate(0, 0, 70))
	if err != nil || created != 1 {
		t.Fatalf("first record = %d, %v", created, err)
	}
	created, err = RecordPriceActionPhaseSnapshots(context.Background(), db, []string{"CYCLE"}, base.AddDate(0, 0, 70))
	if err != nil || created != 0 {
		t.Fatalf("repeat record = %d, %v", created, err)
	}
}

func priceActionTestRows(count int, adjusted bool) []PriceSnapshot {
	rows := make([]PriceSnapshot, 0, count)
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		close := int64(10_000_000 + i*50_000)
		rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, i), OpenMicros: close - 30_000, HighMicros: close + 180_000, LowMicros: close - 180_000, CloseMicros: close, Volume: 100_000, Adjusted: adjusted, QualityStatus: QualityStatusValid})
	}
	return rows
}

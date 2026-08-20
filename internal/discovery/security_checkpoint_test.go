package discovery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSecurityUniverseRetryResumesCommittedCheckpoints(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{
		CIK: "0000004321", SourceKey: "RESUME", Ticker: "RESUME", ProviderTicker: "RESUME",
		CompanyName: "Resume Co", SecurityName: "Resume Co Common Stock", Exchange: "Nasdaq",
		SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", MappingStatus: MappingStatusCurrent,
	}
	hookCalls := map[string]int{}
	coordinator := Coordinator{
		DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "resume", now)},
		Shares: fakeShareSource{version: testSourceVersion("shares", "resume", now)}, Events: noEvents(now), Clock: func() time.Time { return now },
		AfterStageChunk: func(phase string, _ int) error {
			hookCalls[phase]++
			if phase == "historical-shares" {
				return errors.New("simulated stop after committed historical-share chunk")
			}
			return nil
		},
	}
	failed, err := coordinator.SyncSecurityUniverse(context.Background())
	if err == nil || failed.Status != BatchStatusFailed {
		t.Fatalf("first batch=%#v err=%v", failed, err)
	}
	var committed SecurityStageCheckpoint
	if err := db.First(&committed, "batch_id = ? AND phase = ? AND chunk = 0", failed.BatchID, "historical-shares").Error; err != nil {
		t.Fatal(err)
	}
	if committed.Status != securityCheckpointCompleted || committed.AttemptCount != 1 {
		t.Fatalf("committed checkpoint=%#v", committed)
	}

	coordinator.AfterStageChunk = func(phase string, _ int) error {
		hookCalls[phase]++
		return nil
	}
	published, err := coordinator.SyncSecurityUniverse(context.Background())
	if err != nil || published.Status != BatchStatusPublished {
		t.Fatalf("resumed batch=%#v err=%v", published, err)
	}
	if published.BatchID != failed.BatchID {
		t.Fatalf("resumed batch id=%s want=%s", published.BatchID, failed.BatchID)
	}
	if hookCalls["security-listings"] != 1 || hookCalls[BatchKindSecurity] != 1 || hookCalls["historical-shares"] != 1 {
		t.Fatalf("completed chunks ran again: calls=%#v", hookCalls)
	}
	if err := db.First(&committed, committed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if committed.AttemptCount != 1 {
		t.Fatalf("completed checkpoint attempts=%d want=1", committed.AttemptCount)
	}
	summaries, err := ListSecurityStageCheckpointSummaries(context.Background(), db, published.BatchID)
	if err != nil || len(summaries) == 0 {
		t.Fatalf("checkpoint summaries=%#v err=%v", summaries, err)
	}
}

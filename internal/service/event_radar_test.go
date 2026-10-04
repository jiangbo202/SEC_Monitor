package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"sec_monitor/internal/model"
)

func TestEventRadarIncludesVariantsAndPaginatesGlobally(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	forms := []string{}
	for i := 0; i < 25; i++ {
		forms = append(forms, "8-K")
	}
	forms = append(forms, "424B3", "424B5", "SCHEDULE 13D/A", "S-1/A", "S-3ASR", "10-Q")
	for i, form := range forms {
		row := model.Filing{FilingID: fmt.Sprintf("radar-%d", i), Ticker: "TEST", CompanyName: "Fixture", FilingType: form, FilingDate: now.Add(time.Duration(i) * time.Minute)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &FilingService{db: db}
	seen := map[uint]bool{}
	previous := now.Add(time.Hour)
	for page := 1; page <= 3; page++ {
		result, err := s.List(context.Background(), FilingFilter{EventCategory: "major", Page: page, PageSize: 10})
		if err != nil || result.Total != 30 || len(result.Items) != 10 {
			t.Fatalf("page %d: total=%d items=%d error=%v", page, result.Total, len(result.Items), err)
		}
		for _, row := range result.Items {
			if seen[row.ID] || row.FilingDate.After(previous) || row.FilingType == "10-Q" {
				t.Fatalf("invalid global pagination: %+v", row)
			}
			seen[row.ID], previous = true, row.FilingDate
		}
	}
	category, err := s.List(context.Background(), FilingFilter{EventCategory: "13D"})
	if err != nil || category.Total != 1 || category.Items[0].FilingType != "SCHEDULE 13D/A" {
		t.Fatalf("13D category: %+v %v", category, err)
	}
	exact, err := s.List(context.Background(), FilingFilter{FilingType: "424B"})
	if err != nil || exact.Total != 0 {
		t.Fatalf("exact form filter changed: %+v %v", exact, err)
	}
	if _, err := s.List(context.Background(), FilingFilter{EventCategory: "invalid"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid category accepted: %v", err)
	}
}

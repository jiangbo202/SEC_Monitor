package model

import "testing"

func TestIPOOfferingEventTableName(t *testing.T) {
	if got := (IPOOfferingEvent{}).TableName(); got != "ipo_offering_events" {
		t.Fatalf("TableName = %q, want ipo_offering_events", got)
	}
}

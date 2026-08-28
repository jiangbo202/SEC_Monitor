package service

import (
	"testing"

	"sec_monitor/internal/model"
)

func TestDeriveFilingEventUsesStrongest8KItem(t *testing.T) {
	event := deriveFilingEvent(model.Filing{FilingType: "8-K", RawContent: "SEC items: 1.01, 2.02, 9.01"})
	if event.Status != "identified" || event.Category != "业绩与指引" || event.Priority != "高" {
		t.Fatalf("event=%+v", event)
	}
	if len(event.ItemCodes) != 3 || event.ItemCodes[1] != "2.02" {
		t.Fatalf("item codes=%v", event.ItemCodes)
	}
}

func TestDeriveFilingEventDoesNotOverstateUnknown8K(t *testing.T) {
	event := deriveFilingEvent(model.Filing{FilingType: "8-K"})
	if event.Status != "pending" || event.Priority != "待定" || event.Category != "待解析" {
		t.Fatalf("event=%+v", event)
	}
}

func TestDeriveFilingEventClassifiesCapitalRisk(t *testing.T) {
	event := deriveFilingEvent(model.Filing{FilingType: "8-K", RawContent: "SEC items: 3.02"})
	if event.Category != "融资与稀释" || event.Priority != "高" {
		t.Fatalf("event=%+v", event)
	}
}

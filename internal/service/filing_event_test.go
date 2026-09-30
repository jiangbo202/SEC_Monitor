package service

import (
	"strings"
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

func TestDeriveFilingEventClassifiesTradingAndDealFormsWithoutOverstatement(t *testing.T) {
	tests := []struct {
		form, category, factFragment, impactFragment, priority string
	}{
		{"4", "内幕交易", "持股变动", "不能判断方向", "中"},
		{"144", "潜在减持", "拟出售", "不等于交易已经完成", "中"},
		{"425", "并购沟通", "沟通材料", "不代表交易已经获批或完成", "高"},
	}
	for _, test := range tests {
		t.Run(test.form, func(t *testing.T) {
			event := deriveFilingEvent(model.Filing{FilingType: test.form})
			if event.Category != test.category || event.Priority != test.priority || event.Status != "identified" || !strings.Contains(event.Fact, test.factFragment) || !strings.Contains(event.Impact, test.impactFragment) {
				t.Fatalf("event=%+v", event)
			}
		})
	}
}

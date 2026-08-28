package sec

import "testing"

func TestRecentSubmissionsPreserves8KItems(t *testing.T) {
	rows := recentSubmissions{
		AccessionNumber: []string{"0000000001-26-000001"},
		Form:            []string{"8-K"},
		FilingDate:      []string{"2026-08-28"},
		PrimaryDocument: []string{"example.htm"},
		PrimaryDocDesc:  []string{"Current report"},
		Items:           []string{"2.02,9.01"},
	}.toFilings("ACME", "0000000001", "Acme Inc.")
	if len(rows) != 1 || rows[0].RawContent != "SEC items: 2.02,9.01" {
		t.Fatalf("rows=%+v", rows)
	}
}

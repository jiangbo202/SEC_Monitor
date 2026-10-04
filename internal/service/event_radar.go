package service

import (
	"fmt"
	"strings"
)

// Categories are deliberately mapped to complete SEC form names: the general
// filings endpoint retains its exact-form filter, while the radar includes
// amendments and naming variants in one globally sorted, paginated query.
func eventRadarForms(category string) ([]string, error) {
	groups := map[string][]string{
		"8-K":  {"8-K", "8-K/A"},
		"S-1":  {"S-1", "S-1/A"},
		"S-3":  {"S-3", "S-3/A", "S-3ASR"},
		"424B": {"424B1", "424B2", "424B3", "424B4", "424B5", "424B6", "424B7", "424B8"},
		"13D":  {"13D", "13D/A", "SC 13D", "SC 13D/A", "SC13D", "SC13D/A", "SCHEDULE 13D", "SCHEDULE 13D/A"},
	}
	key := strings.ToUpper(strings.TrimSpace(category))
	if key == "MAJOR" {
		forms := []string{}
		for _, group := range []string{"8-K", "S-1", "S-3", "424B", "13D"} {
			forms = append(forms, groups[group]...)
		}
		return forms, nil
	}
	if forms, ok := groups[key]; ok {
		return forms, nil
	}
	return nil, fmt.Errorf("%w: unsupported event category", ErrValidation)
}

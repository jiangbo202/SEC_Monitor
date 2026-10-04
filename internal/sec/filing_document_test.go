package sec

import (
	"context"
	"testing"
)

type indexDocumentFetcher map[string]string

func (fetcher indexDocumentFetcher) FetchFilingDocument(_ context.Context, url string) (string, error) {
	return fetcher[url], nil
}

func TestPrimarySECFilingResolution(t *testing.T) {
	indexURL := "https://www.sec.gov/Archives/edgar/data/123/456/000123-26-000001-index.htm"
	primaryURL := "https://www.sec.gov/Archives/edgar/data/123/456/prospectus.htm"
	index := `<table><tr><td>1</td><td>PROSPECTUS</td><td><a href="/Archives/edgar/data/123/456/prospectus.htm">prospectus.htm</a></td><td>424B4</td></tr><tr><td>2</td><td>GRAPHIC</td><td><a href="other.jpg">other</a></td><td>GRAPHIC</td></tr></table>`
	if got, ok := ResolvePrimaryFilingURL(index, indexURL); !ok || got != primaryURL {
		t.Fatalf("got=%s ok=%v", got, ok)
	}
	body, err := FetchPrimaryFilingDocument(context.Background(), indexDocumentFetcher{indexURL: index, primaryURL: "Original prospectus"}, indexURL)
	if err != nil || body != "Original prospectus" {
		t.Fatalf("body=%s err=%v", body, err)
	}
	for _, href := range []string{"https://evil.test/prospectus.htm", "/Archives/edgar/data/other/prospectus.htm", "000123-26-000001-index.htm"} {
		raw := `<tr><td>1</td><td>Document</td><td><a href="` + href + `">doc</a></td><td>424B4</td></tr>`
		if _, ok := ResolvePrimaryFilingURL("<table>"+raw+"</table>", indexURL); ok {
			t.Fatal("accepted unrelated or recursive document", href)
		}
	}
}

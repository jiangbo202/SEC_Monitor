package sec

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// FetchPrimaryFilingDocument resolves SEC index pages to their sequence-1
// filing. It rejects external links and other accession directories, and keeps
// the index link as the caller's audit reference.
func FetchPrimaryFilingDocument(ctx context.Context, fetcher FilingDocumentFetcher, rawURL string) (string, error) {
	body, err := fetcher.FetchFilingDocument(ctx, rawURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.HasSuffix(strings.ToLower(parsed.Path), "-index.htm") {
		return body, nil
	}
	primary, ok := ResolvePrimaryFilingURL(body, rawURL)
	if !ok {
		return "", errors.New("SEC document index did not contain a verified primary filing")
	}
	return fetcher.FetchFilingDocument(ctx, primary)
}

func ResolvePrimaryFilingURL(raw, indexURL string) (string, bool) {
	base, err := url.Parse(indexURL)
	if err != nil || base.Scheme != "https" || !strings.HasSuffix(base.Hostname(), ".sec.gov") || !strings.HasPrefix(base.Path, "/Archives/edgar/data/") {
		return "", false
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", false
	}
	var primary string
	var text func(*html.Node) string
	text = func(node *html.Node) string {
		if node.Type == html.TextNode {
			return node.Data
		}
		var value strings.Builder
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			value.WriteString(text(child))
		}
		return value.String()
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if primary != "" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "tr" {
			cells := []*html.Node{}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode && child.Data == "td" {
					cells = append(cells, child)
				}
			}
			if len(cells) >= 4 && strings.TrimSpace(text(cells[0])) == "1" {
				kind := strings.TrimSpace(text(cells[3]))
				if kind == "" || kind == "GRAPHIC" || strings.Contains(kind, "XML") {
					return
				}
				var link func(*html.Node)
				link = func(child *html.Node) {
					if child.Type == html.ElementNode && child.Data == "a" {
						for _, attr := range child.Attr {
							if attr.Key != "href" {
								continue
							}
							href, err := url.Parse(attr.Val)
							if err != nil {
								continue
							}
							target := base.ResolveReference(href)
							if value := target.Query().Get("doc"); value != "" {
								target.Path = value
								target.RawQuery = ""
							}
							if target.Scheme == "https" && target.Host == base.Host && strings.HasPrefix(target.Path, path.Dir(base.Path)+"/") && target.Path != base.Path && (strings.HasSuffix(target.Path, ".htm") || strings.HasSuffix(target.Path, ".html") || strings.HasSuffix(target.Path, ".txt")) {
								primary = target.String()
							}
						}
					}
					for nested := child.FirstChild; nested != nil; nested = nested.NextSibling {
						link(nested)
					}
				}
				link(cells[2])
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return primary, primary != ""
}

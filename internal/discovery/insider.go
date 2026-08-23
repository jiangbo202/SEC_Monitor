package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	InsiderParserVersion        = "form4-parser-v2"
	InsiderCoverageVersion      = "form4-coverage-v1"
	form4DocumentRequestTimeout = 30 * time.Second
)

const (
	InsiderCoverageCoveredNoFilings      = "covered_no_filings"
	InsiderCoverageCoveredNoTransactions = "covered_no_transactions"
	InsiderCoverageCoveredTransactions   = "covered_transactions"
	InsiderCoveragePartial               = "partial"
	InsiderCoverageUnavailable           = "unavailable"
)

const (
	InsiderRoleCEO     = "ceo"
	InsiderRoleCFO     = "cfo"
	InsiderRoleFounder = "founder"
	InsiderRoleOther   = "other"
)

const (
	InsiderExclusionDerivative               = "derivative_security"
	InsiderExclusionNotOpenMarketPurchase    = "not_open_market_purchase"
	InsiderExclusionNotAcquired              = "not_acquired"
	InsiderExclusionZeroValue                = "zero_or_missing_value"
	InsiderExclusionNonKeyRole               = "non_key_role"
	InsiderExclusionFounderNeedsConfirmation = "founder_needs_confirmation"
)

type InsiderTransaction struct {
	CIK, Ticker, Accession, SourceURL string
	OwnerName, OfficerTitle, Role     string
	Derivative                        bool
	TransactionDate                   time.Time
	TransactionCode                   string
	AcquiredDisposedCode              string
	Shares                            float64
	PricePerShareUSD                  float64
	ValueUSD                          float64
	SharesOwnedAfter                  float64
	SharesOwnedBefore                 float64
	Qualified                         bool
	ExclusionReason                   string
	FounderConfirmationSuggested      bool
}

type InsiderTransactionSource interface {
	LoadInsiderTransactions(context.Context, map[string]struct{}, time.Time) ([]InsiderTransaction, SourceVersion, error)
}

// InsiderTransactionCoverageSource is an optional extension for sources that
// can prove what was examined. An empty Form 4 result is useful evidence only
// when we know the issuer's eligible filings were actually covered.
type InsiderTransactionCoverageSource interface {
	LoadInsiderTransactionsWithCoverage(context.Context, map[string]struct{}, time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error)
}

type InsiderCoverage struct {
	CIK                       string
	EligibleFilings           int
	DownloadedDocuments       int
	ParsedDocuments           int
	TransactionCount          int
	PermanentDocumentFailures int
	TransientDocumentFailures int
	MalformedDocuments        int
	Status                    string
	CheckedAt                 time.Time
}

type SECForm4InsiderSource struct {
	Metadata        SecurityMetadataSource
	Downloader      *Downloader
	BaseURL         string
	LookbackDays    int
	DocumentTimeout time.Duration
	OnProgress      func(Form4IngestionProgress)
}

type Form4IngestionProgress struct {
	ProcessedIssuers int
	TotalIssuers     int
}

func (s *SECForm4InsiderSource) SetProgressCallback(callback func(Form4IngestionProgress)) {
	if s != nil {
		s.OnProgress = callback
	}
}

func (s SECForm4InsiderSource) LoadInsiderTransactions(ctx context.Context, allowed map[string]struct{}, asOf time.Time) ([]InsiderTransaction, SourceVersion, error) {
	transactions, _, version, err := s.LoadInsiderTransactionsWithCoverage(ctx, allowed, asOf)
	return transactions, version, err
}

func (s SECForm4InsiderSource) LoadInsiderTransactionsWithCoverage(ctx context.Context, allowed map[string]struct{}, asOf time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error) {
	if s.Metadata == nil || s.Downloader == nil {
		return nil, nil, SourceVersion{}, fmt.Errorf("SEC Form 4 metadata source and downloader are required")
	}
	records, metadataVersion, err := s.Metadata.Load(ctx)
	if err != nil {
		return nil, nil, SourceVersion{}, err
	}
	return s.LoadInsiderTransactionsWithMetadata(ctx, records, metadataVersion, allowed, asOf)
}

// LoadInsiderTransactionsWithMetadata reuses the submissions metadata already
// loaded by the security workflow. This avoids reparsing submissions.zip just
// to discover Form 4 accession numbers.
func (s SECForm4InsiderSource) loadInsiderTransactionsWithMetadataLegacy(ctx context.Context, records []SecuritySourceRecord, metadataVersion SourceVersion, allowed map[string]struct{}, asOf time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error) {
	if s.Downloader == nil {
		return nil, nil, SourceVersion{}, fmt.Errorf("SEC Form 4 downloader is required")
	}
	lookback := s.LookbackDays
	if lookback <= 0 {
		lookback = 180
	}
	baseURL := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://www.sec.gov/Archives/edgar/data"
	}
	cutoff := asOf.AddDate(0, 0, -lookback)
	type downloadedDoc struct {
		Filing FilingMetadata
		Result DownloadResult
	}
	downloads := []downloadedDoc{}
	transactions := []InsiderTransaction{}
	coverageByCIK := make(map[string]*InsiderCoverage, len(allowed))
	for cik := range allowed {
		coverageByCIK[cik] = &InsiderCoverage{CIK: cik, Status: InsiderCoverageUnavailable, CheckedAt: asOf}
	}
	processedIssuers := 0
	processedCIKs := make(map[string]struct{}, len(allowed))
	if s.OnProgress != nil {
		s.OnProgress(Form4IngestionProgress{TotalIssuers: len(allowed)})
	}
	for _, record := range records {
		coverage, ok := coverageByCIK[record.CIK]
		if !ok {
			continue
		}
		if _, duplicate := processedCIKs[record.CIK]; duplicate {
			continue
		}
		processedCIKs[record.CIK] = struct{}{}
		// The record itself proves submissions metadata was available for this
		// issuer, even when it contains no Form 4 in the requested window.
		coverage.Status = InsiderCoverageCoveredNoFilings
		for _, filing := range record.FilingMetadata {
			if err := ctx.Err(); err != nil {
				return nil, nil, SourceVersion{}, err
			}
			if filing.CIK != record.CIK {
				continue
			}
			if !eligibleForm4Filing(filing, cutoff, asOf) {
				continue
			}
			coverage.EligibleFilings++
			sourceURL, cacheKey, err := form4DocumentLocation(baseURL, filing)
			if err != nil {
				coverage.MalformedDocuments++
				continue
			}
			// Form 4 attachments are immutable once accessioned. Reusing the
			// local copy avoids thousands of unnecessary SEC requests on every
			// daily universe refresh.
			download, err := s.Downloader.DownloadWithCacheTTL(ctx, sourceURL, cacheKey, nil, -1)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, nil, SourceVersion{}, ctxErr
				}
				if IsDownloadHTTPStatus(err, http.StatusNotFound) || IsDownloadHTTPStatus(err, http.StatusGone) {
					// SEC submissions metadata can retain a historical primary
					// document name after the attachment is removed or replaced.
					// A permanent 404/410 is not a source outage and must not make
					// every daily universe refresh fail.
					coverage.PermanentDocumentFailures++
					continue
				}
				// A historical Form 4 attachment can disappear temporarily or
				// suffer a transient SEC/network failure. Keep the daily universe
				// usable when other ownership documents remain available.
				coverage.TransientDocumentFailures++
				continue
			}
			coverage.DownloadedDocuments++
			parsed, parseErr := parseCachedForm4Document(download, filing.Accession, sourceURL)
			if parseErr != nil {
				// submissions.json commonly names an xslF345* wrapper. SEC serves
				// HTML at that path even though the raw ownership XML is stored at
				// the filing root with the same filename. Retry only this safe,
				// deterministic alternate path before declaring the filing malformed.
				if fallbackURL, fallbackCacheKey, ok := form4RawOwnershipFallbackLocation(baseURL, filing); ok {
					fallback, fallbackErr := s.Downloader.DownloadWithCacheTTL(ctx, fallbackURL, fallbackCacheKey, nil, -1)
					if fallbackErr == nil {
						coverage.DownloadedDocuments++
						parsed, parseErr = parseCachedForm4Document(fallback, filing.Accession, fallbackURL)
						if parseErr == nil {
							sourceURL, download = fallbackURL, fallback
						}
					} else if ctxErr := ctx.Err(); ctxErr != nil {
						return nil, nil, SourceVersion{}, ctxErr
					} else if IsDownloadHTTPStatus(fallbackErr, http.StatusNotFound) || IsDownloadHTTPStatus(fallbackErr, http.StatusGone) {
						coverage.PermanentDocumentFailures++
						continue
					} else {
						coverage.TransientDocumentFailures++
						continue
					}
				}
			}
			if parseErr != nil {
				// One malformed ownership document must not invalidate the whole
				// small-cap universe run.
				coverage.MalformedDocuments++
				continue
			}
			coverage.ParsedDocuments++
			coverage.TransactionCount += len(parsed)
			transactions = append(transactions, parsed...)
			downloads = append(downloads, downloadedDoc{Filing: filing, Result: download})
		}
		processedIssuers++
		if s.OnProgress != nil && (processedIssuers%25 == 0 || processedIssuers == len(allowed)) {
			s.OnProgress(Form4IngestionProgress{ProcessedIssuers: processedIssuers, TotalIssuers: len(allowed)})
		}
	}
	if s.OnProgress != nil && processedIssuers != len(allowed) {
		s.OnProgress(Form4IngestionProgress{ProcessedIssuers: len(allowed), TotalIssuers: len(allowed)})
	}
	coverages := normalizeInsiderCoverage(coverageByCIK)
	sort.Slice(transactions, func(i, j int) bool { return canonicalLess(transactions[i], transactions[j]) })
	versionHash := sha256.New()
	versionHash.Write([]byte(metadataVersion.SHA256 + "\n" + InsiderParserVersion + "\n" + InsiderCoverageVersion + "\n"))
	for _, doc := range downloads {
		versionHash.Write([]byte(doc.Filing.Accession + "\n" + doc.Result.SHA256 + "\n"))
	}
	digest := hex.EncodeToString(versionHash.Sum(nil))
	for _, coverage := range coverages {
		versionHash.Write([]byte(fmt.Sprintf("%s:%s:%d:%d:%d:%d:%d:%d\n", coverage.CIK, coverage.Status, coverage.EligibleFilings, coverage.ParsedDocuments, coverage.TransactionCount, coverage.PermanentDocumentFailures, coverage.TransientDocumentFailures, coverage.MalformedDocuments)))
	}
	digest = hex.EncodeToString(versionHash.Sum(nil))
	version := metadataVersion.Version + "+" + InsiderParserVersion + "+" + InsiderCoverageVersion
	if version == "+"+InsiderParserVersion+"+"+InsiderCoverageVersion {
		version = digest + "+" + InsiderParserVersion + "+" + InsiderCoverageVersion
	}
	return transactions, coverages, SourceVersion{Source: "insiders:sec-form4", Version: version, SHA256: digest, EffectiveAt: metadataVersion.EffectiveAt}, nil
}

// LoadInsiderTransactionsWithMetadata checkpoints groups of issuers after
// their Form 4 documents have been downloaded and parsed. A deadline in a later
// group therefore resumes from the first unfinished group instead of walking
// every issuer again.
func (s SECForm4InsiderSource) LoadInsiderTransactionsWithMetadata(ctx context.Context, records []SecuritySourceRecord, metadataVersion SourceVersion, allowed map[string]struct{}, asOf time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error) {
	if s.Downloader == nil {
		return nil, nil, SourceVersion{}, fmt.Errorf("SEC Form 4 downloader is required")
	}
	lookback := s.LookbackDays
	if lookback <= 0 {
		lookback = 180
	}
	baseURL := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://www.sec.gov/Archives/edgar/data"
	}
	effectiveDate, err := nyCivilDate(asOf)
	if err != nil {
		return nil, nil, SourceVersion{}, err
	}
	cutoff := asOf.AddDate(0, 0, -lookback)
	coverageByCIK := make(map[string]*InsiderCoverage, len(allowed))
	for cik := range allowed {
		coverageByCIK[cik] = &InsiderCoverage{CIK: cik, Status: InsiderCoverageUnavailable, CheckedAt: asOf}
	}
	issuerByCIK := make(map[string]SecuritySourceRecord, len(allowed))
	for _, record := range records {
		if _, ok := allowed[record.CIK]; !ok {
			continue
		}
		if _, exists := issuerByCIK[record.CIK]; !exists {
			issuerByCIK[record.CIK] = record
		}
	}
	issuerCIKs := make([]string, 0, len(issuerByCIK))
	for cik := range issuerByCIK {
		issuerCIKs = append(issuerCIKs, cik)
	}
	sort.Strings(issuerCIKs)
	if s.OnProgress != nil {
		s.OnProgress(Form4IngestionProgress{TotalIssuers: len(issuerCIKs)})
	}
	transactions := []InsiderTransaction{}
	documents := []form4DocumentEvidence{}
	processedIssuers := 0
	for start := 0; start < len(issuerCIKs); start += form4CheckpointChunkSize {
		end := start + form4CheckpointChunkSize
		if end > len(issuerCIKs) {
			end = len(issuerCIKs)
		}
		chunkCIKs := append([]string(nil), issuerCIKs[start:end]...)
		artifactKey, err := form4IssuerChunkKey(metadataVersion.SHA256, effectiveDate, lookback, baseURL, chunkCIKs)
		if err != nil {
			return nil, nil, SourceVersion{}, err
		}
		var artifact form4IssuerChunkArtifact
		resumed := false
		if strings.TrimSpace(s.Downloader.CacheDir) != "" {
			artifact, resumed, err = loadForm4IssuerChunk(s.Downloader.CacheDir, artifactKey, chunkCIKs)
			if err != nil {
				return nil, nil, SourceVersion{}, err
			}
		}
		if !resumed {
			artifact = form4IssuerChunkArtifact{ArtifactKey: artifactKey, CIKs: chunkCIKs}
			cacheable := true
			for _, cik := range chunkCIKs {
				issuerTransactions, coverage, issuerDocuments, issuerErr := s.loadForm4Issuer(ctx, issuerByCIK[cik], asOf, cutoff, baseURL)
				if issuerErr != nil {
					return nil, nil, SourceVersion{}, issuerErr
				}
				if coverage.TransientDocumentFailures > 0 {
					cacheable = false
				}
				artifact.Transactions = append(artifact.Transactions, issuerTransactions...)
				artifact.Coverage = append(artifact.Coverage, coverage)
				artifact.Documents = append(artifact.Documents, issuerDocuments...)
			}
			if cacheable && strings.TrimSpace(s.Downloader.CacheDir) != "" {
				if err := saveForm4IssuerChunk(s.Downloader.CacheDir, artifact); err != nil {
					return nil, nil, SourceVersion{}, err
				}
			}
		}
		transactions = append(transactions, artifact.Transactions...)
		documents = append(documents, artifact.Documents...)
		for i := range artifact.Coverage {
			coverage := artifact.Coverage[i]
			coverage.CheckedAt = asOf
			coverageByCIK[coverage.CIK] = &coverage
		}
		processedIssuers += len(chunkCIKs)
		if s.OnProgress != nil {
			s.OnProgress(Form4IngestionProgress{ProcessedIssuers: processedIssuers, TotalIssuers: len(issuerCIKs)})
		}
	}
	coverages := normalizeInsiderCoverage(coverageByCIK)
	sort.Slice(transactions, func(i, j int) bool { return canonicalLess(transactions[i], transactions[j]) })
	versionHash := sha256.New()
	versionHash.Write([]byte(metadataVersion.SHA256 + "\n" + InsiderParserVersion + "\n" + InsiderCoverageVersion + "\n"))
	for _, document := range documents {
		versionHash.Write([]byte(document.Accession + "\n" + document.SHA256 + "\n"))
	}
	for _, coverage := range coverages {
		versionHash.Write([]byte(fmt.Sprintf("%s:%s:%d:%d:%d:%d:%d:%d\n", coverage.CIK, coverage.Status, coverage.EligibleFilings, coverage.ParsedDocuments, coverage.TransactionCount, coverage.PermanentDocumentFailures, coverage.TransientDocumentFailures, coverage.MalformedDocuments)))
	}
	digest := hex.EncodeToString(versionHash.Sum(nil))
	version := metadataVersion.Version + "+" + InsiderParserVersion + "+" + InsiderCoverageVersion
	if version == "+"+InsiderParserVersion+"+"+InsiderCoverageVersion {
		version = digest + "+" + InsiderParserVersion + "+" + InsiderCoverageVersion
	}
	return transactions, coverages, SourceVersion{Source: "insiders:sec-form4", Version: version, SHA256: digest, EffectiveAt: metadataVersion.EffectiveAt}, nil
}

func (s SECForm4InsiderSource) loadForm4Issuer(ctx context.Context, record SecuritySourceRecord, asOf, cutoff time.Time, baseURL string) ([]InsiderTransaction, InsiderCoverage, []form4DocumentEvidence, error) {
	coverage := InsiderCoverage{CIK: record.CIK, Status: InsiderCoverageCoveredNoFilings, CheckedAt: asOf}
	transactions := []InsiderTransaction{}
	documents := []form4DocumentEvidence{}
	documentTimeout := s.DocumentTimeout
	if documentTimeout <= 0 {
		documentTimeout = form4DocumentRequestTimeout
	}
	for _, filing := range record.FilingMetadata {
		if err := ctx.Err(); err != nil {
			return nil, InsiderCoverage{}, nil, err
		}
		if filing.CIK != record.CIK || !eligibleForm4Filing(filing, cutoff, asOf) {
			continue
		}
		coverage.EligibleFilings++
		locations, err := preferredForm4DocumentLocations(baseURL, filing)
		if err != nil {
			coverage.MalformedDocuments++
			continue
		}
		var parsed []InsiderTransaction
		var download DownloadResult
		parsedOK := false
		for index, location := range locations {
			documentCtx, cancel := context.WithTimeout(ctx, documentTimeout)
			candidate, downloadErr := s.Downloader.DownloadWithCacheTTL(documentCtx, location.URL, location.CacheKey, nil, -1)
			cancel()
			if downloadErr != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, InsiderCoverage{}, nil, ctxErr
				}
				if index == len(locations)-1 {
					if IsDownloadHTTPStatus(downloadErr, http.StatusNotFound) || IsDownloadHTTPStatus(downloadErr, http.StatusGone) {
						coverage.PermanentDocumentFailures++
					} else {
						coverage.TransientDocumentFailures++
					}
				}
				continue
			}
			coverage.DownloadedDocuments++
			candidateParsed, parseErr := parseCachedForm4Document(candidate, filing.Accession, location.URL)
			if parseErr != nil {
				if index == len(locations)-1 {
					coverage.MalformedDocuments++
				}
				continue
			}
			parsed, download, parsedOK = candidateParsed, candidate, true
			break
		}
		if !parsedOK {
			continue
		}
		coverage.ParsedDocuments++
		coverage.TransactionCount += len(parsed)
		transactions = append(transactions, parsed...)
		documents = append(documents, form4DocumentEvidence{Accession: filing.Accession, SHA256: download.SHA256})
	}
	return transactions, coverage, documents, nil
}

type form4DocumentCandidate struct {
	URL      string
	CacheKey string
}

// preferredForm4DocumentLocations requests the accession-root ownership XML
// before SEC's rendered xslF345 wrapper. Most filings expose both, so this
// avoids downloading and parsing an HTML wrapper for every document.
func preferredForm4DocumentLocations(baseURL string, filing FilingMetadata) ([]form4DocumentCandidate, error) {
	originalURL, originalCacheKey, err := form4DocumentLocation(baseURL, filing)
	if err != nil {
		return nil, err
	}
	locations := []form4DocumentCandidate{}
	if rawURL, rawCacheKey, ok := form4RawOwnershipFallbackLocation(baseURL, filing); ok {
		locations = append(locations, form4DocumentCandidate{URL: rawURL, CacheKey: rawCacheKey})
	}
	locations = append(locations, form4DocumentCandidate{URL: originalURL, CacheKey: originalCacheKey})
	return locations, nil
}

func parseCachedForm4Document(download DownloadResult, accession, sourceURL string) ([]InsiderTransaction, error) {
	file, err := os.Open(download.Path)
	if err != nil {
		return nil, err
	}
	parsed, parseErr := ParseForm4OwnershipXML(file, accession, sourceURL)
	closeErr := file.Close()
	if parseErr != nil {
		return nil, parseErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return parsed, nil
}

func normalizeInsiderCoverage(byCIK map[string]*InsiderCoverage) []InsiderCoverage {
	result := make([]InsiderCoverage, 0, len(byCIK))
	for _, coverage := range byCIK {
		if coverage == nil {
			continue
		}
		if coverage.EligibleFilings == 0 {
			if coverage.Status != InsiderCoverageUnavailable {
				coverage.Status = InsiderCoverageCoveredNoFilings
			}
		} else if coverage.ParsedDocuments == 0 && (coverage.PermanentDocumentFailures > 0 || coverage.TransientDocumentFailures > 0 || coverage.MalformedDocuments > 0) {
			coverage.Status = InsiderCoverageUnavailable
		} else if coverage.PermanentDocumentFailures > 0 || coverage.TransientDocumentFailures > 0 || coverage.MalformedDocuments > 0 {
			coverage.Status = InsiderCoveragePartial
		} else if coverage.TransactionCount == 0 {
			coverage.Status = InsiderCoverageCoveredNoTransactions
		} else {
			coverage.Status = InsiderCoverageCoveredTransactions
		}
		result = append(result, *coverage)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CIK < result[j].CIK })
	return result
}

func ParseForm4OwnershipXML(r io.Reader, accession, sourceURL string) ([]InsiderTransaction, error) {
	var doc ownershipDocument
	decoder := xml.NewDecoder(io.LimitReader(r, 10<<20))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode Form 4 ownership XML: %w", err)
	}
	form := strings.ToUpper(strings.TrimSpace(doc.DocumentType))
	if form != "4" && form != "4/A" {
		return nil, fmt.Errorf("unsupported ownership document type %q", doc.DocumentType)
	}
	cik := normalizeCIKString(doc.Issuer.CIK)
	if cik == "" {
		return nil, fmt.Errorf("missing issuer CIK")
	}
	owner := doc.ReportingOwner
	role, founderSuggested := normalizeInsiderRole(owner.Relationship.OfficerTitle)
	base := InsiderTransaction{
		CIK: cik, Ticker: strings.ToUpper(strings.TrimSpace(doc.Issuer.TradingSymbol)), Accession: accession, SourceURL: sourceURL,
		OwnerName: strings.TrimSpace(owner.ID.Name), OfficerTitle: strings.TrimSpace(owner.Relationship.OfficerTitle), Role: role,
		FounderConfirmationSuggested: founderSuggested,
	}
	out := make([]InsiderTransaction, 0, len(doc.NonDerivativeTable.Transactions)+len(doc.DerivativeTable.Transactions))
	for _, row := range doc.NonDerivativeTable.Transactions {
		tx, err := ownershipTransactionToInsider(base, row, false)
		if err != nil {
			return nil, err
		}
		out = append(out, qualifyInsiderTransaction(tx))
	}
	for _, row := range doc.DerivativeTable.Transactions {
		tx, err := ownershipTransactionToInsider(base, row, true)
		if err != nil {
			return nil, err
		}
		out = append(out, qualifyInsiderTransaction(tx))
	}
	return out, nil
}

func eligibleForm4Filing(filing FilingMetadata, cutoff, asOf time.Time) bool {
	form := strings.ToUpper(strings.TrimSpace(filing.Form))
	if form != "4" && form != "4/A" {
		return false
	}
	if strings.TrimSpace(filing.PrimaryDocument) == "" || strings.TrimSpace(filing.Accession) == "" || !validCIK(filing.CIK) {
		return false
	}
	filed := filing.FiledAt
	if filed.IsZero() {
		filed = filing.AcceptedAt
	}
	if filed.IsZero() || filed.Before(cutoff) || filed.After(asOf) {
		return false
	}
	return true
}

func form4DocumentLocation(baseURL string, filing FilingMetadata) (string, string, error) {
	if !validCIK(filing.CIK) {
		return "", "", fmt.Errorf("invalid Form 4 CIK")
	}
	accession := strings.TrimSpace(filing.Accession)
	archiveCIK, ok := form4AccessionCIK(accession)
	if !ok {
		return "", "", fmt.Errorf("invalid Form 4 accession")
	}
	primary, ok := safeForm4PrimaryDocument(filing.PrimaryDocument)
	if !ok {
		return "", "", fmt.Errorf("invalid Form 4 primary document")
	}
	noDash := strings.ReplaceAll(accession, "-", "")
	if noDash == "" || strings.ContainsAny(noDash, `/\`) {
		return "", "", fmt.Errorf("invalid Form 4 accession")
	}
	// A Form 4 can be listed under both the issuer and a reporting owner. SEC's
	// archive path always belongs to the filer encoded in the accession number,
	// which is not necessarily filing.CIK (the issuer CIK from submissions data).
	cikPath := strings.TrimLeft(archiveCIK, "0")
	sourceURL := strings.TrimRight(baseURL, "/") + "/" + cikPath + "/" + noDash + "/" + escapeForm4DocumentPath(primary)
	cacheKey := "form4-" + archiveCIK + "-" + noDash + "-" + sanitizeForm4CachePart(primary)
	if !safeCacheKey(cacheKey) {
		return "", "", fmt.Errorf("invalid Form 4 cache key")
	}
	return sourceURL, cacheKey, nil
}

func form4AccessionCIK(accession string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(accession), "-")
	if len(parts) != 3 || len(parts[0]) != 10 || len(parts[1]) != 2 || len(parts[2]) != 6 {
		return "", false
	}
	for _, part := range parts {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return "", false
			}
		}
	}
	cik := normalizeCIKString(parts[0])
	return cik, validCIK(cik)
}

// form4RawOwnershipFallbackLocation turns SEC's rendered XSL path into the
// raw XML filename at the accession root. It never accepts an arbitrary path:
// only a two-or-more segment xslF345*/filename primary document qualifies.
func form4RawOwnershipFallbackLocation(baseURL string, filing FilingMetadata) (string, string, bool) {
	primary, ok := safeForm4PrimaryDocument(filing.PrimaryDocument)
	if !ok {
		return "", "", false
	}
	parts := strings.Split(primary, "/")
	if len(parts) < 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "xslf345") {
		return "", "", false
	}
	fallback := parts[len(parts)-1]
	copy := filing
	copy.PrimaryDocument = fallback
	sourceURL, cacheKey, err := form4DocumentLocation(baseURL, copy)
	if err != nil {
		return "", "", false
	}
	return sourceURL, cacheKey + "-raw", true
}

// safeForm4PrimaryDocument accepts SEC's common xslF345*/document.xml paths
// while rejecting path traversal and unsafe EDGAR archive path segments.
func safeForm4PrimaryDocument(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return "", false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
		for i := 0; i < len(part); i++ {
			ch := part[i]
			if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '-' && ch != '_' && ch != '.' {
				return "", false
			}
		}
	}
	return value, true
}

func escapeForm4DocumentPath(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func sanitizeForm4CachePart(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' {
			b.WriteByte(ch)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

type ownershipDocument struct {
	DocumentType string `xml:"documentType"`
	Issuer       struct {
		CIK           string `xml:"issuerCik"`
		TradingSymbol string `xml:"issuerTradingSymbol"`
	} `xml:"issuer"`
	ReportingOwner struct {
		ID struct {
			Name string `xml:"rptOwnerName"`
		} `xml:"reportingOwnerId"`
		Relationship struct {
			IsOfficer    string `xml:"isOfficer"`
			OfficerTitle string `xml:"officerTitle"`
		} `xml:"reportingOwnerRelationship"`
	} `xml:"reportingOwner"`
	NonDerivativeTable struct {
		Transactions []ownershipTransaction `xml:"nonDerivativeTransaction"`
	} `xml:"nonDerivativeTable"`
	DerivativeTable struct {
		Transactions []ownershipTransaction `xml:"derivativeTransaction"`
	} `xml:"derivativeTable"`
}

type ownershipTransaction struct {
	TransactionDate struct {
		Value string `xml:"value"`
	} `xml:"transactionDate"`
	TransactionCoding struct {
		Code string `xml:"transactionCode"`
	} `xml:"transactionCoding"`
	TransactionAmounts struct {
		Shares struct {
			Value string `xml:"value"`
		} `xml:"transactionShares"`
		Price struct {
			Value string `xml:"value"`
		} `xml:"transactionPricePerShare"`
		AcquiredDisposed struct {
			Value string `xml:"value"`
		} `xml:"transactionAcquiredDisposedCode"`
	} `xml:"transactionAmounts"`
	PostTransactionAmounts struct {
		SharesOwnedFollowingTransaction struct {
			Value string `xml:"value"`
		} `xml:"sharesOwnedFollowingTransaction"`
	} `xml:"postTransactionAmounts"`
}

func ownershipTransactionToInsider(base InsiderTransaction, row ownershipTransaction, derivative bool) (InsiderTransaction, error) {
	tx := base
	tx.Derivative = derivative
	date, err := time.Parse(time.DateOnly, strings.TrimSpace(row.TransactionDate.Value))
	if err != nil {
		return InsiderTransaction{}, fmt.Errorf("invalid Form 4 transaction date")
	}
	tx.TransactionDate = date
	tx.TransactionCode = strings.ToUpper(strings.TrimSpace(row.TransactionCoding.Code))
	tx.AcquiredDisposedCode = strings.ToUpper(strings.TrimSpace(row.TransactionAmounts.AcquiredDisposed.Value))
	var ok bool
	if tx.Shares, ok = parsePositiveForm4Decimal(row.TransactionAmounts.Shares.Value); !ok {
		tx.Shares = 0
	}
	if tx.PricePerShareUSD, ok = parsePositiveForm4Decimal(row.TransactionAmounts.Price.Value); !ok {
		tx.PricePerShareUSD = 0
	}
	if tx.SharesOwnedAfter, ok = parsePositiveForm4Decimal(row.PostTransactionAmounts.SharesOwnedFollowingTransaction.Value); !ok {
		tx.SharesOwnedAfter = 0
	}
	tx.ValueUSD = tx.Shares * tx.PricePerShareUSD
	if tx.SharesOwnedAfter > 0 && tx.Shares > 0 {
		tx.SharesOwnedBefore = tx.SharesOwnedAfter - tx.Shares
		if tx.SharesOwnedBefore < 0 {
			tx.SharesOwnedBefore = 0
		}
	}
	return tx, nil
}

func qualifyInsiderTransaction(tx InsiderTransaction) InsiderTransaction {
	switch {
	case tx.Derivative:
		tx.ExclusionReason = InsiderExclusionDerivative
	case tx.TransactionCode != "P":
		tx.ExclusionReason = InsiderExclusionNotOpenMarketPurchase
	case tx.AcquiredDisposedCode != "A":
		tx.ExclusionReason = InsiderExclusionNotAcquired
	case tx.ValueUSD <= 0:
		tx.ExclusionReason = InsiderExclusionZeroValue
	case tx.Role == InsiderRoleFounder:
		tx.ExclusionReason = InsiderExclusionFounderNeedsConfirmation
	case tx.Role != InsiderRoleCEO && tx.Role != InsiderRoleCFO:
		tx.ExclusionReason = InsiderExclusionNonKeyRole
	default:
		tx.Qualified = true
	}
	return tx
}

func normalizeInsiderRole(title string) (string, bool) {
	title = strings.ToLower(strings.TrimSpace(title))
	compact := strings.NewReplacer(".", "", ",", "", "-", " ").Replace(title)
	switch {
	case strings.Contains(compact, "chief executive officer") || strings.Contains(compact, " ceo") || compact == "ceo":
		return InsiderRoleCEO, false
	case strings.Contains(compact, "chief financial officer") || strings.Contains(compact, " cfo") || compact == "cfo":
		return InsiderRoleCFO, false
	case strings.Contains(compact, "founder"):
		return InsiderRoleFounder, true
	default:
		return InsiderRoleOther, false
	}
}

func parsePositiveForm4Decimal(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return 0, false
	}
	f, _ := r.Float64()
	return f, true
}

func InsiderTransactionToSnapshot(securityID uint, tx InsiderTransaction, createdAt time.Time) InsiderTransactionSnapshot {
	row := InsiderTransactionSnapshot{
		SecurityID: securityID, Accession: tx.Accession, OwnerName: tx.OwnerName, OfficerTitle: tx.OfficerTitle, Role: tx.Role,
		Derivative: tx.Derivative, TransactionDate: tx.TransactionDate, TransactionCode: tx.TransactionCode, AcquiredDisposedCode: tx.AcquiredDisposedCode,
		SharesMicros: decimalFloatToMicros(tx.Shares), PriceMicros: decimalFloatToMicros(tx.PricePerShareUSD), ValueMicros: decimalFloatToMicros(tx.ValueUSD),
		SharesOwnedAfterMicros: decimalFloatToMicros(tx.SharesOwnedAfter), SharesOwnedBeforeMicros: decimalFloatToMicros(tx.SharesOwnedBefore),
		Qualified: tx.Qualified, ExclusionReason: tx.ExclusionReason, FounderConfirmationSuggested: tx.FounderConfirmationSuggested,
		ParserVersion: InsiderParserVersion, SourceURL: tx.SourceURL, CreatedAt: createdAt,
	}
	row.IdentitySHA256 = insiderTransactionSnapshotIdentity(row)
	return row
}

func insiderTransactionSnapshotIdentity(row InsiderTransactionSnapshot) string {
	canonical := strings.Join([]string{
		strings.TrimSpace(row.Accession),
		strings.TrimSpace(row.OwnerName),
		row.TransactionDate.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(row.TransactionCode),
		strings.TrimSpace(row.AcquiredDisposedCode),
		strconv.FormatInt(row.SharesMicros, 10),
		strconv.FormatInt(row.PriceMicros, 10),
		strconv.FormatInt(row.SharesOwnedAfterMicros, 10),
		strconv.FormatInt(row.SharesOwnedBeforeMicros, 10),
		strconv.FormatBool(row.Derivative),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func decimalFloatToMicros(value float64) int64 {
	return int64(math.Round(value * 1_000_000))
}

func normalizeCIKString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 10 {
		return ""
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	if len(value) < 10 {
		value = strings.Repeat("0", 10-len(value)) + value
	}
	return value
}

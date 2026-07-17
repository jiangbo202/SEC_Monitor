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
	"strings"
	"time"
)

const InsiderParserVersion = "form4-parser-v1"

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

type SECForm4InsiderSource struct {
	Metadata     SecurityMetadataSource
	Downloader   *Downloader
	BaseURL      string
	LookbackDays int
}

func (s SECForm4InsiderSource) LoadInsiderTransactions(ctx context.Context, allowed map[string]struct{}, asOf time.Time) ([]InsiderTransaction, SourceVersion, error) {
	if s.Metadata == nil || s.Downloader == nil {
		return nil, SourceVersion{}, fmt.Errorf("SEC Form 4 metadata source and downloader are required")
	}
	records, metadataVersion, err := s.Metadata.Load(ctx)
	if err != nil {
		return nil, SourceVersion{}, err
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
	eligibleDownloads := 0
	failedDownloads := 0
	var lastDownloadErr error
	for _, record := range records {
		if _, ok := allowed[record.CIK]; !ok {
			continue
		}
		for _, filing := range record.FilingMetadata {
			if _, ok := allowed[filing.CIK]; !ok {
				continue
			}
			if !eligibleForm4Filing(filing, cutoff, asOf) {
				continue
			}
			sourceURL, cacheKey, err := form4DocumentLocation(baseURL, filing)
			if err != nil {
				continue
			}
			eligibleDownloads++
			// Form 4 attachments are immutable once accessioned. Reusing the
			// local copy avoids thousands of unnecessary SEC requests on every
			// daily universe refresh.
			download, err := s.Downloader.DownloadWithCacheTTL(ctx, sourceURL, cacheKey, nil, -1)
			if err != nil {
				if IsDownloadHTTPStatus(err, http.StatusNotFound) || IsDownloadHTTPStatus(err, http.StatusGone) {
					// SEC submissions metadata can retain a historical primary
					// document name after the attachment is removed or replaced.
					// A permanent 404/410 is not a source outage and must not make
					// every daily universe refresh fail.
					continue
				}
				// A historical Form 4 attachment can disappear temporarily or
				// suffer a transient SEC/network failure. Keep the daily universe
				// usable when other ownership documents remain available.
				failedDownloads++
				lastDownloadErr = err
				continue
			}
			file, err := os.Open(download.Path)
			if err != nil {
				return nil, SourceVersion{}, err
			}
			parsed, parseErr := ParseForm4OwnershipXML(file, filing.Accession, sourceURL)
			closeErr := file.Close()
			if parseErr != nil {
				// EDGAR's submissions metadata occasionally points at an HTML
				// wrapper or a non-ownership attachment. One malformed filing must
				// not invalidate the whole small-cap universe run.
				continue
			}
			if closeErr != nil {
				return nil, SourceVersion{}, closeErr
			}
			transactions = append(transactions, parsed...)
			downloads = append(downloads, downloadedDoc{Filing: filing, Result: download})
		}
	}
	if eligibleDownloads > 0 && len(downloads) == 0 && failedDownloads > 0 {
		return nil, SourceVersion{}, fmt.Errorf("download all eligible Form 4 documents: %w", lastDownloadErr)
	}
	sort.Slice(transactions, func(i, j int) bool { return canonicalLess(transactions[i], transactions[j]) })
	versionHash := sha256.New()
	versionHash.Write([]byte(metadataVersion.SHA256 + "\n" + InsiderParserVersion + "\n"))
	for _, doc := range downloads {
		versionHash.Write([]byte(doc.Filing.Accession + "\n" + doc.Result.SHA256 + "\n"))
	}
	digest := hex.EncodeToString(versionHash.Sum(nil))
	version := metadataVersion.Version + "+" + InsiderParserVersion
	if version == "+"+InsiderParserVersion {
		version = digest + "+" + InsiderParserVersion
	}
	return transactions, SourceVersion{Source: "insiders:sec-form4", Version: version, SHA256: digest, EffectiveAt: metadataVersion.EffectiveAt}, nil
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
	if accession == "" {
		return "", "", fmt.Errorf("missing Form 4 accession")
	}
	primary, ok := safeForm4PrimaryDocument(filing.PrimaryDocument)
	if !ok {
		return "", "", fmt.Errorf("invalid Form 4 primary document")
	}
	noDash := strings.ReplaceAll(accession, "-", "")
	if noDash == "" || strings.ContainsAny(noDash, `/\`) {
		return "", "", fmt.Errorf("invalid Form 4 accession")
	}
	cikPath := strings.TrimLeft(filing.CIK, "0")
	sourceURL := strings.TrimRight(baseURL, "/") + "/" + cikPath + "/" + noDash + "/" + escapeForm4DocumentPath(primary)
	cacheKey := "form4-" + filing.CIK + "-" + noDash + "-" + sanitizeForm4CachePart(primary)
	if !safeCacheKey(cacheKey) {
		return "", "", fmt.Errorf("invalid Form 4 cache key")
	}
	return sourceURL, cacheKey, nil
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
	return InsiderTransactionSnapshot{
		SecurityID: securityID, Accession: tx.Accession, OwnerName: tx.OwnerName, OfficerTitle: tx.OfficerTitle, Role: tx.Role,
		Derivative: tx.Derivative, TransactionDate: tx.TransactionDate, TransactionCode: tx.TransactionCode, AcquiredDisposedCode: tx.AcquiredDisposedCode,
		SharesMicros: decimalFloatToMicros(tx.Shares), PriceMicros: decimalFloatToMicros(tx.PricePerShareUSD), ValueMicros: decimalFloatToMicros(tx.ValueUSD),
		SharesOwnedAfterMicros: decimalFloatToMicros(tx.SharesOwnedAfter), SharesOwnedBeforeMicros: decimalFloatToMicros(tx.SharesOwnedBefore),
		Qualified: tx.Qualified, ExclusionReason: tx.ExclusionReason, FounderConfirmationSuggested: tx.FounderConfirmationSuggested,
		ParserVersion: InsiderParserVersion, SourceURL: tx.SourceURL, CreatedAt: createdAt,
	}
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

package discovery

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/big"
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

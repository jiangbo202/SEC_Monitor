package discovery

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Fundamental jobs operate on US issuers, not SDK ticker-only special lists.
// Passing an explicit counter avoids STI.US -> IX/SG/STI and ticker reuse
// such as SPCX.US -> ETF/US/SPCX. Quote APIs still use provider symbols.
func explicitUSStockCounterID(symbol string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if !strings.HasSuffix(symbol, ".US") {
		return "", fmt.Errorf("fundamental issuer identity must be a US equity: %s", symbol)
	}
	ticker := strings.TrimSuffix(symbol, ".US")
	if ticker == "" || strings.HasPrefix(ticker, ".") || strings.ContainsAny(ticker, "/ \\.") {
		return "", fmt.Errorf("ambiguous equity identity: %s", symbol)
	}
	return "ST/US/" + ticker, nil
}

// Preserve suspect legacy observations for audit, but exclude them from
// current research and change comparisons until explicitly identified.
func verifiedIssuerSnapshots(db *gorm.DB) *gorm.DB {
	return db.Where("ticker NOT IN ? OR identity_counter_id = 'ST/US/' || ticker", []string{"STI", "SPCX"})
}

func issuerCounterID(ticker string) string {
	id, _ := explicitUSStockCounterID(normalizeAnalystRatingTicker(ticker) + ".US")
	return id
}

package handler

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type insiderPlanBackfillSEC interface {
	sec.Client
	sec.FilingDocumentFetcher
}

type insiderPlanBackfillResult struct {
	ScopeTickers              int                 `json:"scope_tickers"`
	PendingForm4Documents     int                 `json:"pending_form4_documents"`
	ParsedForm4Documents      int                 `json:"parsed_form4_documents"`
	FailedForm4Documents      int                 `json:"failed_form4_documents"`
	UpdatedTransactions       int64               `json:"updated_transactions"`
	ConfirmedPlanTransactions int64               `json:"confirmed_plan_transactions"`
	ReporterCIKsChecked       int                 `json:"reporter_ciks_checked"`
	ParsedForm144Documents    int                 `json:"parsed_form144_documents"`
	RegisteredPlans           int64               `json:"registered_plans"`
	Warnings                  []string            `json:"warnings"`
	Coverage                  insiderPlanCoverage `json:"coverage"`
}

type insiderBackfillDocument struct {
	SecurityID uint
	Accession  string
	SourceURL  string
}

const insiderForm144ParserVersion = "form144-plan-parser-v1"

type insiderPlanDocumentReceipt struct {
	Accession       string
	SourceURL       string
	DisclosureCount int
}

// BackfillInsiderTradingPlans reparses only locally persisted Form 4 evidence
// that has not yet been covered by the current parser. It is intentionally
// limited to current small-cap candidates and enabled watch targets.
func (h *AppHandler) BackfillInsiderTradingPlans(c *gin.Context) {
	client, ok := h.SEC.(insiderPlanBackfillSEC)
	if !ok || client == nil {
		Error(c, fmt.Errorf("SEC filing document client is unavailable"))
		return
	}
	h.insiderPlanBackfillMu.Lock()
	if h.insiderPlanBackfilling {
		h.insiderPlanBackfillMu.Unlock()
		Error(c, fmt.Errorf("10b5-1 history backfill is already running"))
		return
	}
	h.insiderPlanBackfilling = true
	h.insiderPlanBackfillMu.Unlock()
	defer func() {
		h.insiderPlanBackfillMu.Lock()
		h.insiderPlanBackfilling = false
		h.insiderPlanBackfillMu.Unlock()
	}()

	scopeTickers, err := h.currentInsiderScopeTickers(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	result, err := h.backfillInsiderPlanHistory(context.WithoutCancel(c.Request.Context()), client, scopeTickers, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) backfillInsiderPlanHistory(ctx context.Context, client insiderPlanBackfillSEC, scopeTickers []string, now time.Time) (insiderPlanBackfillResult, error) {
	result := insiderPlanBackfillResult{ScopeTickers: len(scopeTickers), Warnings: []string{}}
	if h.DiscoveryDB == nil || len(scopeTickers) == 0 {
		coverage, err := h.insiderPlanCoverage(ctx, scopeTickers, "")
		result.Coverage = coverage
		return result, err
	}
	securityScope := `security_id IN (
		SELECT security_id FROM listings WHERE ticker IN ?
		UNION SELECT security_id FROM security_batch_identities WHERE ticker IN ?
	)`
	var documents []insiderBackfillDocument
	if err := h.DiscoveryDB.WithContext(ctx).Model(&discovery.InsiderTransactionSnapshot{}).
		Select("security_id, accession, source_url").
		Where(securityScope, scopeTickers, scopeTickers).
		Where("source_url <> '' AND COALESCE(parser_version, '') <> ?", discovery.InsiderParserVersion).
		Group("security_id, accession, source_url").Order("security_id, accession, source_url").Find(&documents).Error; err != nil {
		return result, err
	}
	result.PendingForm4Documents = len(documents)
	securityIDs := make([]uint, 0, len(documents))
	securitySeen := map[uint]struct{}{}
	for _, document := range documents {
		if _, ok := securitySeen[document.SecurityID]; !ok {
			securitySeen[document.SecurityID] = struct{}{}
			securityIDs = append(securityIDs, document.SecurityID)
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !isSECArchiveDocumentURL(document.SourceURL) {
			result.FailedForm4Documents++
			appendInsiderBackfillWarning(&result, fmt.Sprintf("跳过非 SEC 原文地址：%s", document.SourceURL))
			continue
		}
		body, err := client.FetchFilingDocument(ctx, document.SourceURL)
		if err != nil {
			result.FailedForm4Documents++
			appendInsiderBackfillWarning(&result, fmt.Sprintf("%s：%v", document.Accession, err))
			continue
		}
		parsed, err := discovery.ParseForm4OwnershipXML(strings.NewReader(body), document.Accession, document.SourceURL)
		if err != nil || len(parsed) == 0 {
			result.FailedForm4Documents++
			if err == nil {
				err = fmt.Errorf("ownership document contains no transactions")
			}
			appendInsiderBackfillWarning(&result, fmt.Sprintf("%s：%v", document.Accession, err))
			continue
		}
		if err := h.persistInsiderBackfillDocument(ctx, document, parsed, now); err != nil {
			return result, err
		}
		result.ParsedForm4Documents++
		result.UpdatedTransactions += int64(len(parsed))
	}

	if len(securityIDs) == 0 {
		if err := h.DiscoveryDB.WithContext(ctx).Model(&discovery.InsiderTransactionSnapshot{}).
			Where(securityScope, scopeTickers, scopeTickers).Distinct("security_id").Pluck("security_id", &securityIDs).Error; err != nil {
			return result, err
		}
	}
	if err := discovery.ReconcileInsiderTradingPlans(h.DiscoveryDB.WithContext(ctx), securityIDs, now); err != nil {
		return result, err
	}
	disclosures, receipts, reportersChecked, parsed144, warnings, err := h.backfillInsiderForm144(ctx, client, scopeTickers, now)
	if err != nil {
		return result, err
	}
	result.ReporterCIKsChecked = reportersChecked
	result.ParsedForm144Documents = parsed144
	for _, warning := range warnings {
		appendInsiderBackfillWarning(&result, warning)
	}
	if len(disclosures) > 0 {
		securityIDByCIK, err := h.insiderScopeSecurityIDsByCIK(ctx, scopeTickers)
		if err != nil {
			return result, err
		}
		if err := discovery.UpsertInsiderPlanDisclosures(h.DiscoveryDB.WithContext(ctx), securityIDByCIK, disclosures, now); err != nil {
			return result, err
		}
	}
	if err := h.persistInsiderPlanDocumentReceipts(ctx, receipts, now); err != nil {
		return result, err
	}
	coverage, err := h.insiderPlanCoverage(ctx, scopeTickers, "")
	if err != nil {
		return result, err
	}
	result.Coverage = coverage
	result.ConfirmedPlanTransactions = coverage.ConfirmedPlanTransactions
	result.RegisteredPlans = coverage.RegisteredPlans
	if _, notifyErr := service.CreateTenB5OnePlanDiscoveryNotifications(ctx, h.DB, h.DiscoveryDB, h.InAppNotifications, now); notifyErr != nil {
		appendInsiderBackfillWarning(&result, fmt.Sprintf("创建 10b5-1 首次发现站内通知失败：%v", notifyErr))
	}
	return result, nil
}

func (h *AppHandler) persistInsiderBackfillDocument(ctx context.Context, document insiderBackfillDocument, parsed []discovery.InsiderTransaction, now time.Time) error {
	first := parsed[0]
	return h.DiscoveryDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"reporting_owner_cik":           first.ReportingOwnerCIK,
			"is_ten_b5_one":                 first.IsTenB5One,
			"ten_b5_one_status":             first.TenB5OneStatus,
			"ten_b5_one_plan_adoption_date": first.TenB5OnePlanAdoptionDate,
			"ten_b5_one_evidence_source":    first.TenB5OneEvidenceSource,
			"ten_b5_one_evidence":           first.TenB5OneEvidence,
			"parser_version":                discovery.InsiderParserVersion,
		}
		if err := tx.Model(&discovery.InsiderTransactionSnapshot{}).
			Where("security_id = ? AND accession = ?", document.SecurityID, document.Accession).Updates(updates).Error; err != nil {
			return err
		}
		rows := make([]discovery.InsiderTransactionSnapshot, 0, len(parsed))
		for _, transaction := range parsed {
			rows = append(rows, discovery.InsiderTransactionToSnapshot(document.SecurityID, transaction, now))
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "security_id"}, {Name: "identity_sha256"}},
			DoUpdates: clause.AssignmentColumns([]string{"reporting_owner_cik", "is_ten_b5_one", "ten_b5_one_status", "ten_b5_one_plan_adoption_date", "ten_b5_one_evidence_source", "ten_b5_one_evidence", "parser_version", "source_url"}),
		}).Create(&rows).Error
	})
}

func (h *AppHandler) backfillInsiderForm144(ctx context.Context, client insiderPlanBackfillSEC, scopeTickers []string, now time.Time) ([]discovery.InsiderPlanDisclosure, []insiderPlanDocumentReceipt, int, int, []string, error) {
	securityScope := `security_id IN (
		SELECT security_id FROM listings WHERE ticker IN ?
		UNION SELECT security_id FROM security_batch_identities WHERE ticker IN ?
	)`
	var reporterCIKs []string
	if err := h.DiscoveryDB.WithContext(ctx).Model(&discovery.InsiderTransactionSnapshot{}).
		Where(securityScope, scopeTickers, scopeTickers).
		Where("parser_version = ? AND reporting_owner_cik <> '' AND (is_ten_b5_one = ? OR ten_b5_one_status = ?)", discovery.InsiderParserVersion, true, discovery.TenB5OneStatusPossible).
		Distinct("reporting_owner_cik").Pluck("reporting_owner_cik", &reporterCIKs).Error; err != nil {
		return nil, nil, 0, 0, nil, err
	}
	sort.Strings(reporterCIKs)
	var existingAccessions []string
	if err := h.DiscoveryDB.WithContext(ctx).Model(&discovery.InsiderPlanDocumentReceipt{}).
		Where("source_form = ? AND parser_version = ?", "144", insiderForm144ParserVersion).
		Distinct("accession").Pluck("accession", &existingAccessions).Error; err != nil {
		return nil, nil, 0, 0, nil, err
	}
	existing := map[string]struct{}{}
	for _, accession := range existingAccessions {
		existing[accession] = struct{}{}
	}
	cutoff := now.AddDate(-2, 0, 0)
	disclosures := []discovery.InsiderPlanDisclosure{}
	receipts := []insiderPlanDocumentReceipt{}
	warnings := []string{}
	parsedDocuments := 0
	for _, reporterCIK := range reporterCIKs {
		if err := ctx.Err(); err != nil {
			return nil, nil, 0, 0, nil, err
		}
		filings, err := client.ListFilings(ctx, sec.FilingQuery{CIK: reporterCIK})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("申报人 CIK %s：%v", reporterCIK, err))
			continue
		}
		for _, filing := range filings {
			form := strings.ToUpper(strings.TrimSpace(filing.FilingType))
			if form != "144" && form != "144/A" || filing.FilingDate.Before(cutoff) || filing.FilingDate.After(now) {
				continue
			}
			if _, ok := existing[filing.AccessionNumber]; ok || !isSECArchiveDocumentURL(filing.FilingURL) {
				continue
			}
			body, err := client.FetchFilingDocument(ctx, filing.FilingURL)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Form 144 %s：%v", filing.AccessionNumber, err))
				continue
			}
			sourceURL := filing.FilingURL
			parsed, err := discovery.ParseForm144PlanXML(strings.NewReader(body), filing.AccessionNumber, sourceURL)
			if err != nil {
				// SEC submissions sometimes exposes the rendered XSL path as the
				// primary document (xsl144*/primary_doc.xml). That endpoint returns
				// HTML despite its .xml suffix; the raw XML lives at the accession
				// root with the same filename.
				if rawURL, ok := rawSECXMLFallbackURL(sourceURL, "xsl144"); ok {
					rawBody, fetchErr := client.FetchFilingDocument(ctx, rawURL)
					if fetchErr == nil {
						if rawParsed, parseErr := discovery.ParseForm144PlanXML(strings.NewReader(rawBody), filing.AccessionNumber, rawURL); parseErr == nil {
							parsed, err, sourceURL = rawParsed, nil, rawURL
						} else {
							err = parseErr
						}
					} else {
						err = fetchErr
					}
				}
			}
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Form 144 %s：%v", filing.AccessionNumber, err))
				continue
			}
			disclosures = append(disclosures, parsed...)
			receipts = append(receipts, insiderPlanDocumentReceipt{Accession: filing.AccessionNumber, SourceURL: sourceURL, DisclosureCount: len(parsed)})
			parsedDocuments++
			existing[filing.AccessionNumber] = struct{}{}
		}
	}
	return disclosures, receipts, len(reporterCIKs), parsedDocuments, warnings, nil
}

func (h *AppHandler) persistInsiderPlanDocumentReceipts(ctx context.Context, receipts []insiderPlanDocumentReceipt, now time.Time) error {
	if len(receipts) == 0 {
		return nil
	}
	rows := make([]discovery.InsiderPlanDocumentReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.Accession) == "" {
			continue
		}
		rows = append(rows, discovery.InsiderPlanDocumentReceipt{SourceForm: "144", Accession: receipt.Accession, SourceURL: receipt.SourceURL, ParserVersion: insiderForm144ParserVersion, DisclosureCount: receipt.DisclosureCount, ParsedAt: now})
	}
	if len(rows) == 0 {
		return nil
	}
	return h.DiscoveryDB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_form"}, {Name: "accession"}},
		DoUpdates: clause.AssignmentColumns([]string{"source_url", "parser_version", "disclosure_count", "parsed_at"}),
	}).Create(&rows).Error
}

func rawSECXMLFallbackURL(value, xslPrefix string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || !strings.EqualFold(parsed.Hostname(), "www.sec.gov") {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || !strings.HasPrefix(strings.ToLower(parts[len(parts)-2]), strings.ToLower(xslPrefix)) {
		return "", false
	}
	filename := parts[len(parts)-1]
	if _, ok := safeSECArchivePathPart(filename); !ok || !strings.HasSuffix(strings.ToLower(filename), ".xml") {
		return "", false
	}
	parts = append(parts[:len(parts)-2], filename)
	parsed.Path = "/" + strings.Join(parts, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	rawURL := parsed.String()
	return rawURL, rawURL != value && isSECArchiveDocumentURL(rawURL)
}

func safeSECArchivePathPart(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "", false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '-' && ch != '_' && ch != '.' {
			return "", false
		}
	}
	return value, true
}

func (h *AppHandler) insiderScopeSecurityIDsByCIK(ctx context.Context, scopeTickers []string) (map[string]uint, error) {
	var securities []discovery.Security
	err := h.DiscoveryDB.WithContext(ctx).Where(`id IN (
		SELECT security_id FROM listings WHERE ticker IN ?
		UNION SELECT security_id FROM security_batch_identities WHERE ticker IN ?
	)`, scopeTickers, scopeTickers).Find(&securities).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]uint, len(securities))
	for _, security := range securities {
		result[strings.TrimSpace(security.CIK)] = security.ID
	}
	return result, nil
}

func isSECArchiveDocumentURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return (host == "sec.gov" || strings.HasSuffix(host, ".sec.gov")) && strings.HasPrefix(parsed.Path, "/Archives/edgar/data/")
}

func appendInsiderBackfillWarning(result *insiderPlanBackfillResult, warning string) {
	if result == nil || strings.TrimSpace(warning) == "" || len(result.Warnings) >= 20 {
		return
	}
	result.Warnings = append(result.Warnings, warning)
}

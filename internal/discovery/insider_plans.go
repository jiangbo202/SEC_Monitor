package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	InsiderPlanStatusActive     = "active"
	InsiderPlanStatusExecuting  = "executing"
	InsiderPlanStatusTerminated = "terminated"
	InsiderPlanStatusExpired    = "expired"
	InsiderPlanStatusUnknown    = "unknown"

	InsiderPlanConfidenceConfirmed = "confirmed"
	InsiderPlanConfidenceProbable  = "probable"
	InsiderPlanLinkUnlinked        = "unlinked"

	InsiderPlanEventAdoption    = "adoption"
	InsiderPlanEventAmendment   = "amendment"
	InsiderPlanEventTermination = "termination"
	InsiderPlanEventExecution   = "execution"
	InsiderPlanEventSaleNotice  = "sale_notice"
)

type InsiderPlanDisclosure struct {
	CIK, OwnerName, OfficerTitle string
	AdoptionDate                 time.Time
	NoticeDate                   time.Time
	ProposedSaleDate             *time.Time
	ProposedShares               float64
	ProposedMarketValueUSD       float64
	SourceForm, Accession        string
	SourceURL, Evidence          string
}

type form144Document struct {
	FormData struct {
		IssuerInfo struct {
			CIK           string   `xml:"issuerCik"`
			OwnerName     string   `xml:"nameOfPersonForWhoseAccountTheSecuritiesAreToBeSold"`
			Relationships []string `xml:"relationshipsToIssuer>relationshipToIssuer"`
		} `xml:"issuerInfo"`
		Securities []struct {
			UnitsSold            string `xml:"noOfUnitsSold"`
			AggregateMarketValue string `xml:"aggregateMarketValue"`
			ApproxSaleDate       string `xml:"approxSaleDate"`
		} `xml:"securitiesInformation"`
		NoticeSignature struct {
			NoticeDate        string   `xml:"noticeDate"`
			PlanAdoptionDates []string `xml:"planAdoptionDates>planAdoptionDate"`
		} `xml:"noticeSignature"`
	} `xml:"formData"`
}

// ParseForm144PlanXML follows the SEC Form 144 XML 2.0 schema. The proposed
// sale quantity is evidence for a notice, not assumed to be the plan's total
// authorization.
func ParseForm144PlanXML(r io.Reader, accession, sourceURL string) ([]InsiderPlanDisclosure, error) {
	var doc form144Document
	if err := xml.NewDecoder(io.LimitReader(r, 10<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode Form 144 XML: %w", err)
	}
	cik := normalizeCIKString(doc.FormData.IssuerInfo.CIK)
	owner := strings.TrimSpace(doc.FormData.IssuerInfo.OwnerName)
	if cik == "" || owner == "" {
		return nil, fmt.Errorf("Form 144 issuer CIK and reporting person are required")
	}
	noticeDate, _ := parseForm144Date(doc.FormData.NoticeSignature.NoticeDate)
	var proposedShares, proposedValue float64
	var proposedDate *time.Time
	for _, item := range doc.FormData.Securities {
		shares, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(item.UnitsSold), ",", ""), 64)
		value, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(item.AggregateMarketValue), ",", ""), 64)
		proposedShares += shares
		proposedValue += value
		if proposedDate == nil {
			if parsed, ok := parseForm144Date(item.ApproxSaleDate); ok {
				proposedDate = &parsed
			}
		}
	}
	role := strings.Join(doc.FormData.IssuerInfo.Relationships, ", ")
	seen := map[string]struct{}{}
	result := []InsiderPlanDisclosure{}
	for _, raw := range doc.FormData.NoticeSignature.PlanAdoptionDates {
		adopted, ok := parseForm144Date(raw)
		if !ok {
			continue
		}
		key := adopted.Format(time.DateOnly)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, InsiderPlanDisclosure{CIK: cik, OwnerName: owner, OfficerTitle: role, AdoptionDate: adopted, NoticeDate: noticeDate, ProposedSaleDate: proposedDate, ProposedShares: proposedShares, ProposedMarketValueUSD: proposedValue, SourceForm: "144", Accession: accession, SourceURL: sourceURL, Evidence: fmt.Sprintf("Form 144 discloses a Rule 10b5-1 plan adoption date of %s", key)})
	}
	return result, nil
}

func parseForm144Date(value string) (time.Time, bool) {
	for _, layout := range []string{"01/02/2006", time.DateOnly} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func normalizeInsiderOwnerKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func insiderPlanIdentity(securityID uint, ownerKey string, adoptionDate time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s", securityID, normalizeInsiderOwnerKey(ownerKey), adoptionDate.UTC().Format("2006-01-02"))))
	return hex.EncodeToString(sum[:])
}

func insiderPlanEventIdentity(planID uint, transactionIdentity string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|execution|%s", planID, transactionIdentity)))
	return hex.EncodeToString(sum[:])
}

func insiderPlanDisclosureEventIdentity(planID uint, disclosure InsiderPlanDisclosure) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|sale_notice|%s|%s", planID, disclosure.Accession, disclosure.AdoptionDate.Format(time.DateOnly))))
	return hex.EncodeToString(sum[:])
}

// UpsertInsiderPlanDisclosures records Form 144 plan evidence before Form 4
// execution reconciliation. It intentionally does not map the notice's
// proposed sale amount to MaximumSharesMicros.
func UpsertInsiderPlanDisclosures(db *gorm.DB, securityIDByCIK map[string]uint, disclosures []InsiderPlanDisclosure, now time.Time) error {
	if db == nil || len(disclosures) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, disclosure := range disclosures {
			securityID, ok := securityIDByCIK[disclosure.CIK]
			if !ok || disclosure.AdoptionDate.IsZero() || normalizeInsiderOwnerKey(disclosure.OwnerName) == "" {
				continue
			}
			identity := insiderPlanIdentity(securityID, disclosure.OwnerName, disclosure.AdoptionDate)
			plan := InsiderTradingPlan{SecurityID: securityID, IdentitySHA256: identity, OwnerKey: normalizeInsiderOwnerKey(disclosure.OwnerName), OwnerName: disclosure.OwnerName, OfficerTitle: disclosure.OfficerTitle, AdoptionDate: disclosure.AdoptionDate, Status: InsiderPlanStatusActive, EvidenceConfidence: InsiderPlanConfidenceConfirmed, PrimarySourceForm: disclosure.SourceForm, PrimarySourceAccession: disclosure.Accession, PrimarySourceURL: disclosure.SourceURL, EvidenceSummary: disclosure.Evidence, CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "identity_sha256"}}, DoNothing: true}).Create(&plan).Error; err != nil {
				return err
			}
			if plan.ID == 0 {
				if err := tx.Where("identity_sha256 = ?", identity).First(&plan).Error; err != nil {
					return err
				}
			}
			updates := map[string]any{"evidence_confidence": InsiderPlanConfidenceConfirmed, "updated_at": now}
			if plan.PrimarySourceForm != "144" {
				updates["primary_source_form"] = "144"
				updates["primary_source_accession"] = disclosure.Accession
				updates["primary_source_url"] = disclosure.SourceURL
				updates["evidence_summary"] = disclosure.Evidence
			}
			if err := tx.Model(&InsiderTradingPlan{}).Where("id = ?", plan.ID).Updates(updates).Error; err != nil {
				return err
			}
			eventDate := disclosure.NoticeDate
			if eventDate.IsZero() {
				eventDate = disclosure.AdoptionDate
			}
			event := InsiderTradingPlanEvent{PlanID: plan.ID, SecurityID: securityID, IdentitySHA256: insiderPlanDisclosureEventIdentity(plan.ID, disclosure), EventType: InsiderPlanEventSaleNotice, EventDate: eventDate, SourceForm: "144", Accession: disclosure.Accession, SourceURL: disclosure.SourceURL, Evidence: disclosure.Evidence, Confidence: InsiderPlanConfidenceConfirmed, SharesMicros: decimalFloatToMicros(disclosure.ProposedShares), ValueMicros: decimalFloatToMicros(disclosure.ProposedMarketValueUSD), CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "identity_sha256"}}, DoNothing: true}).Create(&event).Error; err != nil {
				return err
			}
			if err := refreshInsiderPlanAggregate(tx, plan.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReconcileInsiderTradingPlans materializes confirmed Form 4 plan executions
// into a stable plan registry. Transactions without an explicitly disclosed
// adoption date remain unlinked so two different plans are never silently
// merged.
func ReconcileInsiderTradingPlans(db *gorm.DB, securityIDs []uint, now time.Time) error {
	if db == nil || len(securityIDs) == 0 {
		return nil
	}
	ids := append([]uint(nil), securityIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ids = compactUintIDs(ids)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&InsiderTransactionSnapshot{}).
			Where("security_id IN ? AND is_ten_b5_one = ? AND ten_b5_one_plan_adoption_date IS NULL", ids, true).
			Updates(map[string]any{"ten_b5_one_plan_id": nil, "ten_b5_one_link_confidence": InsiderPlanLinkUnlinked}).Error; err != nil {
			return err
		}
		var rows []InsiderTransactionSnapshot
		if err := tx.Where("security_id IN ? AND is_ten_b5_one = ? AND ten_b5_one_plan_adoption_date IS NOT NULL", ids, true).
			Order("transaction_date ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		groups := map[string][]InsiderTransactionSnapshot{}
		for _, row := range rows {
			if row.TenB5OnePlanAdoptionDate == nil || normalizeInsiderOwnerKey(row.OwnerName) == "" {
				continue
			}
			identity := insiderPlanIdentity(row.SecurityID, row.OwnerName, *row.TenB5OnePlanAdoptionDate)
			groups[identity] = append(groups[identity], row)
		}
		identities := make([]string, 0, len(groups))
		for identity := range groups {
			identities = append(identities, identity)
		}
		sort.Strings(identities)
		for _, identity := range identities {
			group := groups[identity]
			first := group[0]
			adoptionDate := first.TenB5OnePlanAdoptionDate.UTC()
			plan := InsiderTradingPlan{
				SecurityID: first.SecurityID, IdentitySHA256: identity,
				OwnerKey: normalizeInsiderOwnerKey(first.OwnerName), OwnerName: first.OwnerName, OfficerTitle: first.OfficerTitle,
				AdoptionDate: adoptionDate, Status: InsiderPlanStatusExecuting, EvidenceConfidence: InsiderPlanConfidenceConfirmed,
				PrimarySourceForm: "4", PrimarySourceAccession: first.Accession, PrimarySourceURL: first.SourceURL,
				EvidenceSummary: first.TenB5OneEvidence, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "identity_sha256"}},
				DoUpdates: clause.AssignmentColumns([]string{"owner_name", "officer_title", "status", "evidence_confidence", "updated_at"}),
			}).Create(&plan).Error; err != nil {
				return err
			}
			if plan.ID == 0 {
				if err := tx.Where("identity_sha256 = ?", identity).First(&plan).Error; err != nil {
					return err
				}
			}
			for _, row := range group {
				if err := tx.Model(&InsiderTransactionSnapshot{}).Where("id = ?", row.ID).Updates(map[string]any{
					"ten_b5_one_plan_id": plan.ID, "ten_b5_one_link_confidence": InsiderPlanConfidenceConfirmed,
				}).Error; err != nil {
					return err
				}
				event := InsiderTradingPlanEvent{
					PlanID: plan.ID, SecurityID: row.SecurityID,
					IdentitySHA256: insiderPlanEventIdentity(plan.ID, row.IdentitySHA256), EventType: InsiderPlanEventExecution,
					EventDate: row.TransactionDate, SourceForm: "4", Accession: row.Accession, SourceURL: row.SourceURL,
					Evidence: row.TenB5OneEvidence, Confidence: InsiderPlanConfidenceConfirmed,
					SharesMicros: row.SharesMicros, ValueMicros: row.ValueMicros, CreatedAt: now,
				}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "identity_sha256"}}, DoNothing: true}).Create(&event).Error; err != nil {
					return err
				}
			}
			if err := refreshInsiderPlanAggregate(tx, plan.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ActiveInsiderPlanTickers returns distinct symbols with a currently active
// or executing evidence-backed 10b5-1 plan. Historical terminated/expired
// plans are deliberately excluded from current-list quick filters.
func ActiveInsiderPlanTickers(ctx context.Context, db *gorm.DB) ([]string, error) {
	if db == nil || ctx == nil {
		return []string{}, nil
	}
	var securityIDs []uint
	if err := db.WithContext(ctx).Model(&InsiderTradingPlan{}).
		Where("status IN ?", []string{InsiderPlanStatusActive, InsiderPlanStatusExecuting}).
		Distinct("security_id").Pluck("security_id", &securityIDs).Error; err != nil {
		return nil, err
	}
	if len(securityIDs) == 0 {
		return []string{}, nil
	}
	resolved := map[uint]bool{}
	seenTicker := map[string]bool{}
	var tickers []string
	var listings []Listing
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("valid_from DESC, id DESC").Find(&listings).Error; err != nil {
		return nil, err
	}
	for _, row := range listings {
		ticker := strings.ToUpper(strings.TrimSpace(row.Ticker))
		if ticker != "" && !resolved[row.SecurityID] {
			resolved[row.SecurityID] = true
			if !seenTicker[ticker] {
				seenTicker[ticker] = true
				tickers = append(tickers, ticker)
			}
		}
	}
	var identities []SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("created_at DESC, id DESC").Find(&identities).Error; err != nil {
		return nil, err
	}
	for _, row := range identities {
		ticker := strings.ToUpper(strings.TrimSpace(row.Ticker))
		if ticker != "" && !resolved[row.SecurityID] {
			resolved[row.SecurityID] = true
			if !seenTicker[ticker] {
				seenTicker[ticker] = true
				tickers = append(tickers, ticker)
			}
		}
	}
	sort.Strings(tickers)
	return tickers, nil
}

func refreshInsiderPlanAggregate(tx *gorm.DB, planID uint, now time.Time) error {
	var plan InsiderTradingPlan
	if err := tx.First(&plan, planID).Error; err != nil {
		return err
	}
	var executions []InsiderTradingPlanEvent
	if err := tx.Where("plan_id = ? AND event_type = ? AND confidence = ?", planID, InsiderPlanEventExecution, InsiderPlanConfidenceConfirmed).
		Order("event_date ASC, id ASC").Find(&executions).Error; err != nil {
		return err
	}
	var shares, value int64
	accessions := map[string]struct{}{}
	for _, event := range executions {
		shares += event.SharesMicros
		value += event.ValueMicros
		accessions[event.Accession] = struct{}{}
	}
	updates := map[string]any{
		"execution_count": len(executions), "evidence_count": len(accessions),
		"executed_shares_micros": shares, "executed_value_micros": value, "updated_at": now,
	}
	if len(executions) > 0 {
		first, last := executions[0].EventDate, executions[len(executions)-1].EventDate
		updates["first_execution_date"] = first
		updates["last_execution_date"] = last
	}
	if plan.MaximumSharesKnown {
		remaining := plan.MaximumSharesMicros - shares
		if remaining < 0 {
			remaining = 0
		}
		updates["remaining_shares_known"] = true
		updates["remaining_shares_micros"] = remaining
	} else {
		updates["remaining_shares_known"] = false
		updates["remaining_shares_micros"] = 0
	}
	return tx.Model(&InsiderTradingPlan{}).Where("id = ?", planID).Updates(updates).Error
}

func compactUintIDs(values []uint) []uint {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

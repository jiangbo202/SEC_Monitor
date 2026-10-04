package service

import (
	"context"
	"gorm.io/gorm"
	"time"
)

// StorageInspection is safe while the server is running. It reads SQLite
// metadata and representative query plans, without scanning credentials,
// deleting history, rebuilding indices or compacting the database.
type StorageInspection struct {
	GeneratedAt    time.Time          `json:"generated_at"`
	PageSize       int64              `json:"page_size"`
	PageCount      int64              `json:"page_count"`
	FreePages      int64              `json:"free_pages"`
	FinancialFacts int64              `json:"financial_facts"`
	Indexes        []StorageIndex     `json:"indexes"`
	QueryPlans     []StorageQueryPlan `json:"query_plans"`
	Notes          []string           `json:"notes"`
}
type StorageIndex struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Bytes      int64  `json:"bytes,omitempty"`
}
type StorageQueryPlan struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

func InspectFinancialStorage(ctx context.Context, db *gorm.DB) (StorageInspection, error) {
	result := StorageInspection{GeneratedAt: time.Now().UTC(), Indexes: []StorageIndex{}, QueryPlans: []StorageQueryPlan{}, Notes: []string{"身份唯一索引保留跨批次去重；不删除财务历史。单列 security_id 与复合索引具有相同前缀，但更小的覆盖索引可能仍有读取收益；仅凭前缀重叠不能判定冗余。"}}
	for _, item := range []struct {
		query  string
		target *int64
	}{{"PRAGMA page_size", &result.PageSize}, {"PRAGMA page_count", &result.PageCount}, {"PRAGMA freelist_count", &result.FreePages}} {
		if err := db.WithContext(ctx).Raw(item.query).Scan(item.target).Error; err != nil {
			return result, err
		}
	}
	if err := db.WithContext(ctx).Table("financial_fact_snapshots").Count(&result.FinancialFacts).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).Raw("SELECT name, sql AS definition FROM sqlite_master WHERE type='index' AND tbl_name='financial_fact_snapshots' ORDER BY name").Scan(&result.Indexes).Error; err != nil {
		return result, err
	}
	var sample struct{ SecurityID uint }
	if err := db.WithContext(ctx).Raw("SELECT security_id FROM financial_fact_snapshots LIMIT 1").Scan(&sample).Error; err != nil {
		return result, err
	}
	for _, spec := range []struct{ name, sql string }{
		{"profit_history", "SELECT * FROM financial_fact_snapshots WHERE security_id=? AND metric='net_income_common' AND quality_status='valid' AND period_end >= '2020-01-01' ORDER BY period_end ASC, filed_at DESC, accepted_at DESC, id DESC"},
		{"valuation", "SELECT * FROM financial_fact_snapshots WHERE security_id=? AND metric IN ('revenue','gross_profit','cash','debt_current','debt_non_current') AND quality_status='valid'"},
		{"incremental_recalculation", "SELECT * FROM financial_fact_snapshots WHERE security_id=? AND quality_status='valid'"},
		{"identity_lookup", "SELECT security_id FROM financial_fact_snapshots WHERE security_id=?"},
	} {
		var plans []struct{ Detail string }
		if err := db.WithContext(ctx).Raw("EXPLAIN QUERY PLAN "+spec.sql, sample.SecurityID).Scan(&plans).Error; err != nil {
			return result, err
		}
		for _, plan := range plans {
			result.QueryPlans = append(result.QueryPlans, StorageQueryPlan{Name: spec.name, Detail: plan.Detail})
		}
	}
	// DBSTAT is optional in SQLite builds. Its absence must not block the
	// portable plan inspection or be mistaken for an empty index.
	var pages []struct {
		Name  string
		Bytes int64
	}
	if err := db.WithContext(ctx).Raw("SELECT name, SUM(pgsize) AS bytes FROM dbstat WHERE name IN (SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='financial_fact_snapshots') GROUP BY name").Scan(&pages).Error; err == nil {
		for index := range result.Indexes {
			for _, page := range pages {
				if result.Indexes[index].Name == page.Name {
					result.Indexes[index].Bytes = page.Bytes
				}
			}
		}
	} else {
		result.Notes = append(result.Notes, "当前 SQLite 构建不提供 DBSTAT 页级大小；查询计划和记录数仍可核验。")
	}
	return result, nil
}

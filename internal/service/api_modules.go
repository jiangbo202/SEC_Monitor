package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
)

var ErrAPIConfigConflict = errors.New("接口配置已被其他窗口修改，请重新读取后再保存")

type APIInterfaceView struct {
	discovery.APIInterfaceDefinition
	Enabled      bool       `json:"enabled"`
	LastStatus   string     `json:"last_status"`
	LastCalledAt *time.Time `json:"last_called_at,omitempty"`
}
type APIModuleView struct {
	discovery.APIModuleDefinition
	Enabled       bool               `json:"enabled"`
	Revision      uint               `json:"revision"`
	Status        string             `json:"status"`
	Interfaces    []APIInterfaceView `json:"interfaces"`
	Warnings      []string           `json:"warnings"`
	SourceOptions []APIModuleSource  `json:"source_options"`
}
type APIModuleSource struct {
	Provider    string                             `json:"provider"`
	Implemented bool                               `json:"implemented"`
	Reason      string                             `json:"reason"`
	Interfaces  []discovery.APIInterfaceDefinition `json:"interfaces"`
}
type APIModuleInput struct {
	Provider      string          `json:"provider"`
	Enabled       *bool           `json:"enabled"`
	Interfaces    map[string]bool `json:"interfaces"`
	Revision      uint            `json:"revision"`
	ConfirmImpact bool            `json:"confirm_impact"`
}

func (s *APIManagementService) ModuleViews(ctx context.Context, providers []APIProviderSummary) ([]APIModuleView, error) {
	rows := []discovery.APIModulePolicy{}
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	policies := map[string]discovery.APIModulePolicy{}
	for _, r := range rows {
		policies[r.Key] = r
	}
	summaries := map[string]APIProviderSummary{}
	for _, p := range providers {
		summaries[p.Policy.Provider] = p
	}
	result := []APIModuleView{}
	for _, def := range discovery.APIModuleDefinitions() {
		row, ok := policies[def.Key]
		if !ok {
			return nil, errors.New("模块配置尚未初始化")
		}
		selected := row.Provider
		if selected == "" {
			selected = def.Provider
		}
		options := []APIModuleSource{}
		for _, provider := range []string{"longbridge", "futu"} {
			alternative, supported := discovery.APIModuleDefinitionForProvider(def.Key, provider)
			reason := "已接入；权限与覆盖需实测"
			if !supported {
				reason = "尚未完成此模块的兼容适配，不可选择"
			}
			options = append(options, APIModuleSource{Provider: provider, Implemented: supported, Reason: reason, Interfaces: alternative.Interfaces})
		}
		def, ok = discovery.APIModuleDefinitionForProvider(def.Key, selected)
		if !ok {
			return nil, ErrValidation
		}
		enabled, err := discovery.ParseAPIInterfaces(row, def)
		if err != nil {
			return nil, err
		}
		v := APIModuleView{APIModuleDefinition: def, Enabled: row.Enabled, Revision: row.Revision, Status: "ready_unverified", Interfaces: []APIInterfaceView{}, Warnings: []string{}}
		v.SourceOptions = options
		p := summaries[def.Provider]
		if !row.Enabled {
			v.Status = "disabled"
		} else if p.Policy.Paused {
			v.Status = "provider_paused"
		} else if !p.CredentialConfigured {
			v.Status = "not_configured"
		}
		if !p.CredentialConfigured {
			v.Warnings = append(v.Warnings, "尚未配置只读凭据；保存配置不会自动授权或发起查询")
		}
		if p.Policy.Paused {
			v.Warnings = append(v.Warnings, "供应商全局暂停优先于模块和接口开关")
		}
		for _, api := range def.Interfaces {
			item := APIInterfaceView{APIInterfaceDefinition: api, Enabled: enabled[api.Key], LastStatus: "not_recorded"}
			var last discovery.APICallRecord
			query := s.db.WithContext(ctx).Where("provider = ? AND endpoint IN ?", def.Provider, api.Endpoints)
			if def.Key == "futu_futures" {
				query = query.Where("UPPER(ticker) LIKE ?", "%MAIN")
			}
			if def.Key == "futu_market" {
				query = query.Where("UPPER(ticker) NOT LIKE ?", "%MAIN")
			}
			err := query.Order("id DESC").First(&last).Error
			if err == nil {
				item.LastStatus = last.Status
				item.LastCalledAt = &last.StartedAt
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			if !item.Enabled {
				v.Warnings = append(v.Warnings, api.Label+"已关闭，现有缓存继续可读")
				if api.Required && row.Enabled {
					v.Status = "dependency_missing"
				}
			}
			v.Interfaces = append(v.Interfaces, item)
		}
		result = append(result, v)
	}
	return result, nil
}
func (s *APIManagementService) UpdateModule(ctx context.Context, key string, in APIModuleInput, operator string) error {
	if in.Provider == "" {
		in.Provider = discovery.APIModuleProvider(ctx, s.db, key)
	}
	def, ok := discovery.APIModuleDefinitionForProvider(key, in.Provider)
	if !ok || in.Enabled == nil || in.Revision == 0 || len(in.Interfaces) != len(def.Interfaces) {
		return fmt.Errorf("%w: 模块或接口参数不完整", ErrValidation)
	}
	allowed := map[string]bool{}
	for _, api := range def.Interfaces {
		allowed[api.Key] = true
		enabled, found := in.Interfaces[api.Key]
		if !found {
			return ErrValidation
		}
		if *in.Enabled && api.Required && !enabled {
			return fmt.Errorf("%w: %s 为必需接口，请先关闭模块，或恢复必需接口", ErrValidation, api.Label)
		}
		if enabled {
			for _, dep := range api.DependsOn {
				if !in.Interfaces[dep] {
					return fmt.Errorf("%w: %s 依赖 %s，不能跳过此顺序", ErrValidation, api.Label, dep)
				}
			}
		}
	}
	for key := range in.Interfaces {
		if !allowed[key] {
			return ErrValidation
		}
	}
	var before discovery.APIModulePolicy
	if err := s.db.WithContext(ctx).First(&before, "key = ?", key).Error; err != nil {
		return err
	}
	if before.Revision != in.Revision {
		return ErrAPIConfigConflict
	}
	old, err := discovery.ParseAPIInterfaces(before, def)
	if err != nil {
		return err
	}
	disable := before.Enabled && !*in.Enabled
	beforeProvider := before.Provider
	if beforeProvider == "" {
		original, _ := discovery.APIModuleDefinitionFor(key)
		beforeProvider = original.Provider
	}
	if beforeProvider != in.Provider {
		disable = true
	}
	for k, v := range in.Interfaces {
		if old[k] && !v {
			disable = true
		}
	}
	if disable && !in.ConfirmImpact {
		return fmt.Errorf("%w: 请确认关闭后受影响的页面与共享任务", ErrValidation)
	}
	b, _ := json.Marshal(in.Interfaces)
	update := s.db.WithContext(ctx).Model(&discovery.APIModulePolicy{}).Where("key = ? AND revision = ?", key, in.Revision).Updates(map[string]any{"enabled": *in.Enabled, "provider": in.Provider, "interfaces_json": string(b), "revision": in.Revision + 1})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return ErrAPIConfigConflict
	}
	return NewAuditService(s.main).Record(ctx, operator, "update", "api_module", key, map[string]any{"provider": beforeProvider, "enabled": before.Enabled, "interfaces": old, "revision": before.Revision}, map[string]any{"provider": in.Provider, "enabled": *in.Enabled, "interfaces": in.Interfaces, "revision": in.Revision + 1})
}

type APIPriceSource struct {
	Key    string `json:"key"`
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
}
type APIPriceRoute struct {
	Configured string           `json:"configured"`
	Order      []string         `json:"order"`
	Editable   bool             `json:"editable"`
	Sources    []APIPriceSource `json:"sources"`
	Scope      string           `json:"scope"`
}
type APIPriceRouteInput struct {
	Order    []string `json:"order"`
	Expected string   `json:"expected"`
}

func priceSourceReady(cfg config.DiscoveryConfig, key string) bool {
	switch key {
	case "futu":
		return cfg.FutuConfigured
	case "longbridge":
		return strings.TrimSpace(cfg.LongbridgeAppKey) != "" && strings.TrimSpace(cfg.LongbridgeAppSecret) != "" && strings.TrimSpace(cfg.LongbridgeAccessToken) != ""
	}
	return false
}
func PriceRouteView(cfg config.DiscoveryConfig) APIPriceRoute {
	v := APIPriceRoute{Configured: cfg.PriceProvider, Order: []string{}, Editable: true, Sources: []APIPriceSource{}, Scope: "仅候选 / 监控行情、历史日线补齐；大盘趋势、财报和机构数据不受此顺序影响。现有按交易日、来源及有效样本验证的回退规则不变；不保证各供应商所有字段等价。"}
	effective := strings.ToLower(strings.TrimSpace(cfg.PriceProvider))
	if effective == "" {
		effective = "stooq"
	}
	for _, k := range strings.Split(effective, ",") {
		k = strings.TrimSpace(k)
		v.Order = append(v.Order, k)
		if k != "longbridge" && k != "futu" {
			v.Editable = false
		}
	}
	for _, k := range []string{"longbridge", "futu"} {
		ready := priceSourceReady(cfg, k)
		reason := "凭据已配置，接口覆盖仍需实测"
		if !ready {
			reason = "缺少凭据，不能加入启用链"
		}
		v.Sources = append(v.Sources, APIPriceSource{Key: k, Ready: ready, Reason: reason})
	}
	return v
}
func (s *APIManagementService) UpdatePriceRoute(ctx context.Context, in APIPriceRouteInput, operator string) error {
	if len(in.Order) == 0 || len(in.Order) > 2 {
		return fmt.Errorf("%w: 至少保留一个已实现的行情数据源", ErrValidation)
	}
	return s.main.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Obtain the SQLite write lock before comparing the effective configuration.
		if err := tx.Exec("UPDATE system_configs SET updated_at = updated_at WHERE config_key = ?", "discovery.price_provider").Error; err != nil {
			return err
		}
		configs := *s.configs
		configs.db = tx
		configs.audit = NewAuditService(tx)
		cfg, err := configs.ApplyDiscoveryConfig(ctx, s.cfg)
		if err != nil {
			return err
		}
		if cfg.PriceProvider != in.Expected {
			return ErrAPIConfigConflict
		}
		if !PriceRouteView(cfg).Editable {
			return fmt.Errorf("%w: 当前为旧行情配置，请先在连接与凭据中迁移，不能在这里静默覆盖", ErrValidation)
		}
		seen := map[string]bool{}
		for _, key := range in.Order {
			if seen[key] || !priceSourceReady(cfg, key) {
				return fmt.Errorf("%w: 重复、未实现或缺少凭据的数据源 %s", ErrValidation, key)
			}
			seen[key] = true
		}
		return configs.UpsertMany(ctx, []ConfigInput{{Key: "discovery.price_provider", Value: strings.Join(in.Order, ","), ValueType: "string", Category: "discovery"}}, operator)
	})
}

// Pure disabled research tasks are skipped before clients are initialized.
// Shared EPS/holder tasks remain runnable while either module is enabled.
func (s *APIManagementService) CheckModuleTask(ctx context.Context, taskName string) error {
	keys := map[string][]string{
		"us_futures_sync":                              {"futu_futures"},
		"futu_institutional_ownership_sync":            {"futu_ownership"},
		"longbridge_institutional_ownership_sync":      {"ownership"},
		"longbridge_candidate_research_sync":           {"eps", "ownership"},
		"longbridge_watch_target_research_sync":        {"eps", "ownership"},
		"longbridge_candidate_valuation_sync":          {"valuation"},
		"longbridge_watch_target_valuation_sync":       {"valuation"},
		"longbridge_candidate_option_research_sync":    {"options"},
		"longbridge_watch_target_option_research_sync": {"options"},
		"market_trend_sync":                            {"market", "temperature"},
		"watch_target_earnings_sync":                   {"calendar"},
	}
	modules, ok := keys[taskName]
	if !ok {
		return nil
	}
	var rows []discovery.APIModulePolicy
	if err := s.db.WithContext(ctx).Where("key IN ?", modules).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.Enabled {
			return nil
		}
	}
	return SkipTask("所依赖接口模块已关闭；保留本地历史，不初始化外部客户端")
}

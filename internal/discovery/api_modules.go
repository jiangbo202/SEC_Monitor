package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAPIDisabled = errors.New("API module or interface disabled")

type ipoCompanyProfileScope struct{}
type futuFuturesScope struct{}

func WithFutuFutures(ctx context.Context) context.Context {
	return context.WithValue(ctx, futuFuturesScope{}, true)
}

// IPO listing verification is a separate, Longbridge-only workflow. It shares
// the profile permission switch, but not the issuer-detail source selection.
func WithLongbridgeIPOCompanyProfile(ctx context.Context) context.Context {
	return context.WithValue(ctx, ipoCompanyProfileScope{}, true)
}

// Only code-owned interface definitions can be configured. No arbitrary URLs,
// credentials, dependency edges or vendor priority lists are accepted here.
type APIInterfaceDefinition struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Endpoints []string `json:"endpoints"`
	Purpose   string   `json:"purpose"`
	Required  bool     `json:"required"`
	DependsOn []string `json:"depends_on"`
}
type APIModuleDefinition struct {
	Key              string                   `json:"key"`
	Label            string                   `json:"label"`
	Provider         string                   `json:"provider"`
	Pages            []string                 `json:"pages"`
	Flow             []string                 `json:"flow"`
	AutoCapabilities []string                 `json:"auto_capabilities"`
	TaskNames        []string                 `json:"task_names"`
	Interfaces       []APIInterfaceDefinition `json:"interfaces"`
	Note             string                   `json:"note"`
}
type APIModulePolicy struct {
	Key            string    `gorm:"primaryKey" json:"key"`
	Provider       string    `gorm:"size:32" json:"provider"`
	Enabled        bool      `json:"enabled"`
	InterfacesJSON string    `gorm:"type:text" json:"-"`
	Revision       uint      `json:"revision"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func APIModuleDefinitions() []APIModuleDefinition {
	api := func(key, label, purpose string, required bool, paths ...string) APIInterfaceDefinition {
		return APIInterfaceDefinition{Key: key, Label: label, Purpose: purpose, Required: required, Endpoints: paths, DependsOn: []string{}}
	}
	return []APIModuleDefinition{
		{Key: "futu_futures", Label: "Futu 美股期货主连", Provider: "futu", Pages: []string{"美股期货"}, Flow: []string{"确认有效主连代码", "历史不复权日线", "本地趋势"}, TaskNames: []string{"us_futures_sync"}, Interfaces: []APIInterfaceDefinition{api("reference", "期货主连资料", "校验期货类型、有效状态和主连代码", true, "/api/v1.0/quote/{symbol}/reference-future"), api("daily", "期货历史日线", "提供方主连 OHLC 与成交量；独立于股票行情顺序", true, "/api/v1.0/quote/{symbol}/history-kline")}, Note: "共用 Futu 授权、全局暂停和每日请求预算；连续合约换月规则由提供方决定。自动更新还需启用美股期货任务。"},
		{Key: "futu_market", Label: "Futu 行情日线", Provider: "futu", Pages: []string{"监控标的", "小盘候选", "价格周期"}, Flow: []string{"美股代码 / 已完成交易日", "不复权日线", "本地技术指标"}, TaskNames: []string{"watch_target_market_sync", "price_action_cycle_replay"}, Interfaces: []APIInterfaceDefinition{api("daily", "历史日线", "不复权 OHLC 与成交股数；通过下方价格链选择主源 / 备源", true, "/api/v1.0/quote/{symbol}/history-kline")}, Note: "与 Longbridge 共用下方价格链顺序；此模块不接管大盘趋势和市场温度。"},
		{Key: "calendar", Label: "财报 / IPO / 宏观日历补充", Provider: "longbridge", Pages: []string{"财报预告", "IPO监控", "宏观日历"}, Flow: []string{"日期范围 / 事件类别", "事件日历", "财报共识补充", "本地事件"}, TaskNames: []string{"watch_target_earnings_sync", "ipo_listing_reconcile_sync", "macro_calendar_sync"}, Interfaces: []APIInterfaceDefinition{api("calendar", "事件日历", "财报、IPO、宏观事件的 Longbridge 补充", true, "/v1/quote/finance_calendar"), api("consensus", "财务共识明细", "财报预告盈利与营收共识", false, "/v1/quote/financial-consensus-detail")}, Note: "共享日历接口关闭会影响三处 Longbridge 补充；SEC IPO 和官方宏观主源仍可运行。自动运行由各自调度任务及原有配置控制。"},
		{Key: "temperature", Label: "市场温度", Provider: "longbridge", Pages: []string{"大盘趋势·市场温度"}, Flow: []string{"美国市场", "当前温度 / 历史温度", "本地记录"}, TaskNames: []string{"market_trend_sync"}, Interfaces: []APIInterfaceDefinition{api("current", "当前市场温度", "提供方美国市场温度", true, "/v1/quote/market_temperature"), api("history", "历史市场温度", "温度历史背景", false, "/v1/quote/history_market_temperature")}},
		{Key: "market", Label: "Longbridge 行情与技术指标", Provider: "longbridge", Pages: []string{"监控标的", "小盘候选", "价格周期", "大盘趋势"}, Flow: []string{"确认美股代码 / 交易日", "报价或历史日线", "本地技术指标计算"}, TaskNames: []string{"watch_target_market_sync", "market_trend_sync", "price_action_cycle_replay"}, Interfaces: []APIInterfaceDefinition{api("quote", "股票报价", "当前报价及日成交量", true, "ws/quote"), api("daily", "历史日线", "技术指标及价格周期样本", true, "ws/history_daily")}, Note: "这里仅控制 Longbridge；候选/监控价格链可继续使用其他已启用备源。大盘趋势直接使用 Longbridge，不受下方备源排序影响。"},
		{Key: "company", Label: "公司资料", Provider: "longbridge", Pages: []string{"标的详情·基本面", "小盘候选·基本面", "IPO监控·上市核验"}, Flow: []string{"确认发行人身份", "公司资料", "本地快照"}, AutoCapabilities: []string{"company"}, Interfaces: []APIInterfaceDefinition{api("profile", "公司资料", "业务介绍及公司背景；不影响 SEC 原始财务", true, "/v1/quote/comp-overview")}, Note: "公司资料接口也用于 IPO 上市核验；公司资料后台开关不控制 IPO 的独立任务。SEC 财务事实不受此开关影响。"},
		{Key: "analyst", Label: "分析师共识", Provider: "longbridge", Pages: []string{"标的详情·估值共识", "小盘候选·估值共识"}, Flow: []string{"确认发行人身份", "最新评级", "共识汇总", "本地快照"}, AutoCapabilities: []string{"analyst"}, Interfaces: []APIInterfaceDefinition{api("latest", "最新评级", "最新机构评级记录", true, "/v1/quote/institution-rating-latest"), api("summary", "评级汇总", "分析师数量与共识目标价", true, "/v1/quote/institution-ratings")}},
		{Key: "eps", Label: "EPS 预期与市场异动", Provider: "longbridge", Pages: []string{"监控标的·研究", "小盘候选·研究"}, Flow: []string{"确认发行人身份", "EPS 预期 / 美国市场异动（独立数据）", "分别保存本地记录"}, AutoCapabilities: []string{"eps", "watch_research"}, TaskNames: []string{"longbridge_candidate_research_sync", "longbridge_watch_target_research_sync"}, Interfaces: []APIInterfaceDefinition{api("eps", "EPS 预期", "市场盈利预测，不替代 SEC 财务事实", true, "/v1/quote/forecast-eps"), api("anomaly", "市场异动", "美国市场异动背景", false, "/v1/quote/changes")}, Note: "与主要股东 / 基金快照共用候选、监控研究任务开关；模块调用开关互相独立。"},
		{Key: "valuation", Label: "估值与同行比较", Provider: "longbridge", Pages: []string{"标的详情·估值共识", "小盘候选·估值共识"}, Flow: []string{"确认发行人身份", "估值", "同行比较", "行业分布", "完整快照"}, AutoCapabilities: []string{"valuation", "watch_valuation"}, TaskNames: []string{"longbridge_candidate_valuation_sync", "longbridge_watch_target_valuation_sync"}, Interfaces: []APIInterfaceDefinition{api("valuation", "估值", "PE / PB / PS 数据", true, "/v1/quote/valuation", "/v1/quote/valuation/detail"), api("peers", "同行比较", "同行估值比较", true, "/v1/quote/industry-valuation-comparison"), api("distribution", "行业分布", "行业估值分位", true, "/v1/quote/industry-valuation-distribution")}, Note: "现有估值写入要求三项完整；必须先关闭模块才能关闭必需接口，防止空数据覆盖旧快照。"},
		{Key: "ownership", Label: "单家主要机构与基金快照", Provider: "longbridge", Pages: []string{"监控标的·机构持仓", "小盘候选·机构持仓", "机构持仓"}, Flow: []string{"确认发行人身份", "主要股东列表", "确认机构类型 / 股东 ID", "机构历史明细", "本地历史"}, AutoCapabilities: []string{"eps", "watch_research"}, TaskNames: []string{"longbridge_institutional_ownership_sync", "longbridge_candidate_research_sync", "longbridge_watch_target_research_sync"}, Interfaces: []APIInterfaceDefinition{api("holders", "主要股东列表", "机构识别与股东 ID", true, "/v1/quote/shareholders"), {Key: "history", Label: "股东历史明细", Purpose: "单家机构持仓变化", Required: true, DependsOn: []string{"holders"}, Endpoints: []string{"/v1/quote/shareholders/holding"}}, api("funds", "基金 / ETF 快照", "基金组合权重；不是公司机构合计比例", false, "/v1/quote/fund-holders")}, Note: "候选 / 监控研究开关同时影响 EPS 和股东快照；独立机构历史任务还需要对应队列开关。与 Futu 合计不互相回退。"},
		{Key: "futu_ownership", Label: "机构合计历史", Provider: "futu", Pages: []string{"监控标的·机构持仓", "小盘候选·机构持仓", "机构持仓"}, Flow: []string{"只读授权", "美股代码", "报告期分页（最多 2 页）", "本地机构合计历史"}, TaskNames: []string{"futu_institutional_ownership_sync"}, Interfaces: []APIInterfaceDefinition{api("aggregate", "机构合计历史", "提供方报告期机构合计，不拼接 Longbridge 明细", true, "/api/v1.0/quote/{symbol}/shareholders/institutional")}, Note: "授权、取消全局暂停和启用后台任务是独立操作。关闭模块不撤销授权，不删除历史。"},
		{Key: "options", Label: "期权与卖空研究", Provider: "longbridge", Pages: []string{"期权研究", "监控标的·期权", "小盘候选·期权"}, Flow: []string{"确认美股代码", "期权成交", "成交失败时历史成交回退；卖空独立补充", "本地研究快照"}, AutoCapabilities: []string{"options"}, TaskNames: []string{"longbridge_candidate_option_research_sync", "longbridge_watch_target_option_research_sync"}, Interfaces: []APIInterfaceDefinition{api("volume", "期权成交", "Call / Put 成交量", true, "ws/option_volume"), {Key: "daily_volume", Label: "历史期权成交", Purpose: "期权成交背景", DependsOn: []string{"volume"}, Endpoints: []string{"ws/option_volume_daily"}}, api("short", "卖空数据", "提供方报告期卖空背景", false, "ws/short_positions")}},
	}
}

// Provider alternatives are limited to adapters with compatible business meaning.
func APIModuleDefinitionForProvider(key, provider string) (APIModuleDefinition, bool) {
	def, ok := APIModuleDefinitionFor(key)
	if !ok {
		return def, false
	}
	if provider == "" || provider == def.Provider {
		return def, true
	}
	if provider != "futu" {
		return def, false
	}
	switch key {
	case "company":
		def.Provider = "futu"
		def.Interfaces[0].Endpoints = []string{"/api/v1.0/quote/{symbol}/company/profile"}
		def.Note = "美股公司资料采用 Futu 属性类型映射；SEC 身份与财务不变。IPO 上市核验仍使用独立 Longbridge 流程。"
		def.Pages = []string{"标的详情·基本面", "小盘候选·基本面"}
	case "analyst":
		def.Provider = "futu"
		for i := range def.Interfaces {
			def.Interfaces[i].Endpoints = []string{"/api/v1.0/quote/{symbol}/research/analyst-consensus"}
			def.Interfaces[i].Purpose = "单次读取富途近三个月共识；评级比例不推算人数"
		}
		def.Note = "美股共识评级比例与目标价单独存储；切换来源不跨供应商计算变化。两项由同一个请求返回。"
	default:
		return def, false
	}
	return def, true
}
func APIModuleProvider(ctx context.Context, db *gorm.DB, key string) string {
	def, ok := APIModuleDefinitionFor(key)
	if !ok {
		return ""
	}
	if db == nil || !db.Migrator().HasTable(&APIModulePolicy{}) {
		return def.Provider
	}
	var row APIModulePolicy
	if db.WithContext(ctx).First(&row, "key = ?", key).Error == nil && row.Provider != "" {
		return row.Provider
	}
	return def.Provider
}
func CheckSelectedAPIModule(ctx context.Context, db *gorm.DB, key string) error {
	if key == "futu_futures" {
		ctx = WithFutuFutures(ctx)
	}
	def, ok := APIModuleDefinitionForProvider(key, APIModuleProvider(ctx, db, key))
	if !ok {
		return errors.New("unsupported module provider")
	}
	for _, api := range def.Interfaces {
		if api.Required {
			if err := CheckAPIModuleEndpoint(ctx, db, def.Provider, api.Endpoints[0]); err != nil {
				return err
			}
		}
	}
	return nil
}
func APIModuleDefinitionFor(key string) (APIModuleDefinition, bool) {
	for _, m := range APIModuleDefinitions() {
		if m.Key == key {
			return m, true
		}
	}
	return APIModuleDefinition{}, false
}
func DefaultAPIInterfaces(def APIModuleDefinition) map[string]bool {
	v := map[string]bool{}
	for _, api := range def.Interfaces {
		v[api.Key] = true
	}
	return v
}
func ParseAPIInterfaces(row APIModulePolicy, def APIModuleDefinition) (map[string]bool, error) {
	v := DefaultAPIInterfaces(def)
	if err := json.Unmarshal([]byte(row.InterfacesJSON), &v); err != nil {
		return nil, err
	}
	return v, nil
}
func EnsureAPIModules(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable(&APIModulePolicy{}) {
		return nil
	} // minimal test fixtures have only the monitor models
	rows := []APIModulePolicy{}
	for _, def := range APIModuleDefinitions() {
		b, _ := json.Marshal(DefaultAPIInterfaces(def))
		rows = append(rows, APIModulePolicy{Key: def.Key, Enabled: true, Revision: 1, InterfacesJSON: string(b)})
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// Query on each admission so a saved switch is effective across server/CLI
// processes immediately, without a stale in-memory configuration cache.
func CheckAPIModuleEndpoint(ctx context.Context, db *gorm.DB, provider, endpoint string) error {
	if db == nil {
		return nil
	}
	for _, def := range APIModuleDefinitions() {
		if provider == "futu" && endpoint == "/api/v1.0/quote/{symbol}/history-kline" {
			if (ctx.Value(futuFuturesScope{}) == true) != (def.Key == "futu_futures") {
				continue
			}
		}
		def, ok := APIModuleDefinitionForProvider(def.Key, provider)
		if !ok {
			continue
		}
		for _, api := range def.Interfaces {
			for _, path := range api.Endpoints {
				if endpoint != path {
					continue
				}
				if !db.Migrator().HasTable(&APIModulePolicy{}) {
					return nil
				}
				var row APIModulePolicy
				err := db.WithContext(ctx).First(&row, "key = ?", def.Key).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				v, err := ParseAPIInterfaces(row, def)
				if err != nil {
					return err
				}
				if !row.Enabled || !v[api.Key] {
					return fmt.Errorf("%w: %s / %s（保留本地缓存）", ErrAPIDisabled, def.Label, api.Label)
				}
				selected := APIModuleProvider(ctx, db, def.Key)
				if def.Key == "company" && provider == "longbridge" && ctx.Value(ipoCompanyProfileScope{}) == true {
					selected = "longbridge"
				}
				if selected != provider {
					return fmt.Errorf("%w: %s 已选择其他供应商", ErrAPIDisabled, def.Label)
				}
			}
		}
	}
	return nil
}

// Avoid initializing shared authentication when the requested data interface is disabled.
func CheckCurrentAPIEndpoint(ctx context.Context, provider, endpoint string) error {
	if m := CurrentAPIMonitor(); m != nil {
		return CheckAPIModuleEndpoint(ctx, m.DB, provider, endpoint)
	}
	return nil
}

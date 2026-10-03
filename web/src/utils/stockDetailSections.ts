// Both entry points share the same vocabulary and reading order.
export const stockDetailSections = [
  { key: 'overview', label: '概览', description: '先确认公司身份与当前状态；候选结论和监控状态使用各自口径。' },
  { key: 'technical', label: '行情技术', description: '本地价格、动能与交易计划；技术信号不是交易指令。' },
  { key: 'fundamentals', label: '基本面', description: '财务表现、业务模型与资本风险。' },
  { key: 'events', label: '事件公告', description: '财报预告、SEC 公告与内幕交易；计划和已发生事实分开阅读。' },
  { key: 'ownership', label: '机构持仓', description: '主要机构各自的持仓变化，不代表全部机构持仓总比例。' },
  { key: 'valuation', label: '估值共识', description: '分析师共识与本地估值情景分开阅读，不构成投资建议。' },
  { key: 'research', label: '研究记录', description: '用户论点与手动 AI 研判，不作为系统事实。' },
  { key: 'data', label: '数据管理', description: '来源、质量、评分依据或监控设置；维护操作集中在此。' }
] as const

export type StockDetailSection = typeof stockDetailSections[number]['key']

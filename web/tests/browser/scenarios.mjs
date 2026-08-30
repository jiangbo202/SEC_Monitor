// One set of UI scenarios for Playwright CI and the in-app browser adapter.
// Only public DOM interactions; no page JS state, network interception or API
// mutation outside the explicitly isolated fixture reset page.
export const fixtureURL = 'http://127.0.0.1:19090'
export async function waitText(ui, text) {
  const deadline = Date.now() + 10000
  while (Date.now() < deadline) {
    if ((await ui.text()).includes(text)) return
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`Expected visible text: ${text}\n${await ui.text()}`)
}
async function reset(ui, scenario='') {
  await ui.goto(`${fixtureURL}/__test/reset?scenario=${scenario}`)
  await waitText(ui, '隔离回归数据已重置')
  await ui.goto(`${fixtureURL}/ticker-workspace?ticker=TEST`)
  await waitText(ui, '研究观点 · TEST')
}
async function click(ui, name, role='button') { await ui.role(role,name).click() }
async function edit(ui, initial=false) {
  await waitText(ui,initial?'尚未建立观点。':'关联证据（保存时快照）')
  await click(ui, initial?'建立研究观点':'编辑 / 复核')
  await waitText(ui,'本次复核结论 / 修订理由')
}
async function fillThesis(ui) {
  await ui.label('研究理由').fill('订单改善，等待下一季验证')
  await ui.label('失效条件').fill('收入同比下降则失效')
  await ui.label('下次验证事项').fill('核对下一季 SEC 收入披露')
  await ui.label('本次复核结论').fill('初始原文支持，保留风险观察')
  await ui.label('下次复核时间').fill('2099-01-01T10:00')
  await click(ui,'SEC 8-K #1','checkbox')
}
export const scenarios = {
  '本地快照回退及通知同页切换': async ui => {
    await reset(ui)
    await waitText(ui, '$12.34')
    await ui.placeholder('NVDA').fill('ALT')
    // Editing the search box must not relabel TEST data as ALT before search.
    await waitText(ui, '研究观点 · TEST')
    await click(ui, '站内消息')
    await ui.role('button',/回归通知：核对 ALT/).click()
    await waitText(ui,'研究观点 · ALT')
    await waitText(ui,'$45.67')
    if ((await ui.text()).includes('$12.34')) throw new Error('Previous ticker price leaked')
  },
  '单模块失败不清空其他数据': async ui => {
    await reset(ui,'partial')
    await waitText(ui,'部分模块暂无数据：机构持仓')
    await waitText(ui,'$12.34')
    await waitText(ui,'TEST 原文证据')
  },
  '观点草稿跟踪失效归档及修订历史': async ui => {
    await reset(ui)
    await edit(ui,true)
    await fillThesis(ui)
    await click(ui,'保存观点')
    await waitText(ui,'草稿 · v1')
    await edit(ui)
    await click(ui,'跟踪中','radio')
    await ui.label('本次复核结论').fill('证据齐全，开始跟踪')
    await click(ui,'保存观点')
    await waitText(ui,'跟踪中 · v2')
    await edit(ui)
    await click(ui,'已失效','radio')
    await ui.label('本次复核结论').fill('后续收入下降，初始判断失效')
    await click(ui,'保存观点')
    await waitText(ui,'已失效 · v3')
    await edit(ui)
    await click(ui,'已归档','radio')
    await ui.label('本次复核结论').fill('结束观察，保留失败复盘')
    await click(ui,'保存观点')
    await waitText(ui,'已归档 · v4')
    await click(ui,'观点修订历史（最近 50 版）')
    await waitText(ui,'v1 · 草稿')
    await waitText(ui,'v2 · 跟踪中')
    await waitText(ui,'后续收入下降，初始判断失效')
  },
  '缺少必填证据不能激活观点': async ui => {
    await reset(ui)
    await edit(ui,true)
    await ui.label('研究理由').fill('不完整观点')
    await ui.label('本次复核结论').fill('缺少证据')
    await click(ui,'跟踪中','radio')
    await click(ui,'保存观点')
    await waitText(ui,'跟踪中观点需要证据')
  },
  '到期队列与新证据提示': async ui => {
    await reset(ui,'due')
    await ui.role('button',/^ALT · /).click()
    await waitText(ui,'研究观点 · ALT')
    await waitText(ui,'已到复核时间')
    await waitText(ui,'1 条新入库证据')
  },
  '读取失败不误建新观点': async ui => {
    await reset(ui,'read-error')
    await waitText(ui,'研究观点读取失败')
    await waitText(ui,'重试读取观点')
    if ((await ui.text()).includes('尚未建立观点。')) throw new Error('Read failure treated as empty')
  },
  '未保存修改离开保护': async ui => {
    await reset(ui)
    await edit(ui,true)
    await ui.label('研究理由').fill('尚未保存的内容')
    await ui.placeholder('NVDA').fill('ALT')
    await click(ui,'打开研究台')
    await waitText(ui,'尚未保存的观点修改将丢失')
    await click(ui,'继续编辑')
    await waitText(ui,'研究观点 · TEST')
  },
}

export async function conflictScenario(ui, other) {
  await reset(ui)
  await edit(ui,true)
  await ui.label('研究理由').fill('第一个窗口的观点')
  await other.goto(`${fixtureURL}/ticker-workspace?ticker=TEST`)
  await waitText(other,'研究观点 · TEST')
  await edit(other,true)
  await other.label('研究理由').fill('第二个窗口的观点')
  await click(ui,'保存观点')
  await waitText(ui,'草稿 · v1')
  await click(other,'保存观点')
  await waitText(other,'研究观点已有新版本')
  await waitText(other,'重新加载最新版本')
}

import { test, expect } from '@playwright/test'
import { scenarios, conflictScenario, fixtureURL } from './scenarios.mjs'

const adapter = page => ({
  goto: url => page.goto(url),
  // Element Plus renders the native radio input at zero size. Click its
  // visible label in headless Chromium instead of forcing a hidden input.
  role: (role,name) => role==='radio' ? page.getByText(name,{exact:true}) : page.getByRole(role,{name,exact:typeof name==='string'}),
  label: name => page.getByLabel(name,{exact:true}),
  placeholder: name => page.getByPlaceholder(name,{exact:true}),
  text: () => page.locator('body').innerText(),
})
const externalRequests = new WeakMap()
const pageErrors = new WeakMap()
test.beforeEach(async ({ context }) => {
  // No fixture test may contact SEC/AI/Telegram or the running user's Docker.
  const attempted=[];externalRequests.set(context,attempted)
  const errors=[];pageErrors.set(context,errors)
  const watch=page=>page.on('pageerror',error=>errors.push(error.message))
  context.pages().forEach(watch);context.on('page',watch)
  await context.route('**/*', route => {
    if(new URL(route.request().url()).origin === fixtureURL)return route.continue()
    attempted.push(route.request().url());return route.abort()
  })
})

test('主要机构历史保留缺失值、隔离日期口径，打开页面不触发外部刷新', async ({page}) => {
  await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
  let refreshes=0
  const point=(overrides={})=>({holder_id:'1',holder_name:'Example Capital',owner_type:'Institution',period:'Q1 2026',holding_date:'',provider_date:'2026-03-31',filing_date:'',percent_of_shares:2,shares_held:1000,source_kind:'detail',source_url:'https://open.longbridge.com/docs/fundamental/fundamental/shareholder-detail',fetched_at:'2026-10-01T00:00:00Z',...overrides})
  const history={ticker:'TEST',institutional_holders:[],fund_holders:[],other_holders:[{holder_name:'Example Person',owner_type:'Person',percent_of_shares:null,report_date:'2026-06-30'}],ownership_history:[point({period:'Q3 2026',provider_date:'2026-09-30',percent_of_shares:3}),point({period:'Q2 2026',provider_date:'2026-06-30',percent_of_shares:null}),point()],history_warnings:[],message:'仅覆盖主要机构，不是机构总占比'}
  await page.route('**/api/institutional-filings',route=>route.fulfill({json:{data:[]}}))
  await page.route('**/api/discovery/institutional-holdings/TEST',route=>route.fulfill({json:{data:history}}))
  await page.route('**/api/discovery/institutional-holdings/TEST/refresh',route=>{refreshes++;return route.fulfill({json:{data:{research:history,refresh:{warnings:[]}}}})})
  await page.goto(`${fixtureURL}/institutional-holdings?ticker=TEST`)
  await expect(page.getByRole('img',{name:/Example Capital 公司持股比例历史/})).toBeVisible()
  await expect(page.getByText('未核验',{exact:true}).first()).toBeVisible()
  expect(refreshes).toBe(0)
  const gap=page.locator('.history-table .el-table__body tr').nth(1)
  await expect(gap).toContainText('Q2 2026')
  await expect(gap).not.toContainText('0.00%')
  await page.locator('.controls').getByText('按提供方记录日期',{exact:true}).click()
  await page.getByRole('option',{name:'按明确持仓截止日',exact:true}).click()
  await expect(page.getByRole('img',{name:/Example Capital 公司持股比例历史/})).toHaveCount(0)
  await expect(page.getByText('等待样本：当前机构在所选日期口径下不足两个有效比例点，暂不绘制趋势。',{exact:true})).toBeVisible()
  await page.getByRole('button',{name:'刷新主要机构历史',exact:true}).click()
  await expect.poll(()=>refreshes).toBe(1)
  await page.getByText(/其他股东：个人 \/ 内部人士 \/ 公司 \/ 类型未确认/).click()
  await expect(page.getByText('Example Person',{exact:true})).toBeVisible()
})
test.afterEach(async ({ request,context }) => {
  expect(externalRequests.get(context)).toEqual([])
  expect(pageErrors.get(context)).toEqual([])
  const status=await (await request.get(`${fixtureURL}/__test/status`)).json()
  expect(status.unexpected_requests).toEqual([])
  expect(status.audits).toBe(status.revisions)
})
for (const [name,run] of Object.entries(scenarios)) {
  test(name,async ({page})=>{await run(adapter(page))})
}
test('并发窗口版本冲突保护',async ({page,context})=>{
  const other=await context.newPage()
  await conflictScenario(adapter(page),adapter(other))
})

test('通知连续切换内幕交易标的及计划页，前进后退同步筛选与数据', async ({page}) => {
  await page.goto(`${fixtureURL}/__test/reset?scenario=inbox-insiders`)
  await page.goto(`${fixtureURL}/ticker-workspace?ticker=TEST`)
  const open = async (ticker,tab) => {
    await page.getByRole('button',{name:'站内消息',exact:true}).click()
    await page.getByRole('button',{name:new RegExp(`回归通知：${ticker} ${tab}`)}).click()
    await expect(page).toHaveURL(`${fixtureURL}/insider-trading?ticker=${ticker}&tab=${tab}`)
    await expect(page.locator('.el-tab-pane:visible .compact-toolbar input').first()).toHaveValue(ticker)
    await expect(page.getByText(`${ticker} ${tab==='plans'?'计划':'交易'}申报人`,{exact:true}).first()).toBeVisible()
  }
  await open('TEST','transactions')
  await open('ALT','transactions')
  await expect(page.getByText('TEST 交易申报人',{exact:true})).toHaveCount(0)
  await open('ALT','plans')
  await open('TEST','plans')
  await page.goBack()
  await expect(page.getByText('ALT 计划申报人',{exact:true}).first()).toBeVisible()
  await page.goBack()
  await expect(page.locator('.compact-toolbar input').first()).toHaveValue('ALT')
  await expect(page.getByText('ALT 交易申报人',{exact:true}).first()).toBeVisible()
  await page.goForward()
  await expect(page.getByText('ALT 计划申报人',{exact:true}).first()).toBeVisible()
})

test('快速连续点击通知时，旧内幕交易响应不会覆盖新标的', async ({page}) => {
  await page.goto(`${fixtureURL}/__test/reset?scenario=inbox-insiders`)
  await page.goto(`${fixtureURL}/ticker-workspace?ticker=TEST`)
  let finishOldResponse
  const oldResponseFinished = new Promise(resolve => { finishOldResponse = resolve })
  let releaseOldResponse
  const mayDeliverOldResponse = new Promise(resolve => { releaseOldResponse = resolve })
  await page.route('**/api/insider-transactions?*', async route => {
    if (new URL(route.request().url()).searchParams.get('ticker') !== 'TEST') return route.continue()
    const response = await route.fetch()
    await mayDeliverOldResponse
    await route.fulfill({response})
    finishOldResponse()
  })
  const open = async ticker => {
    await page.getByRole('button',{name:'站内消息',exact:true}).click()
    await page.getByRole('button',{name:new RegExp(`回归通知：${ticker} transactions`)}).click()
    await expect(page).toHaveURL(new RegExp(`ticker=${ticker}&tab=transactions`))
  }
  await open('TEST')
  await open('ALT')
  try {
    await expect(page.getByText('ALT 交易申报人',{exact:true}).first()).toBeVisible()
  } finally {
    releaseOldResponse()
  }
  await oldResponseFinished
  await expect(page.getByText('ALT 交易申报人',{exact:true}).first()).toBeVisible()
  await expect(page.getByText('TEST 交易申报人',{exact:true})).toHaveCount(0)
})

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

test('模块开关仅在确认保存后生效，固定依赖锁定，行情顺序写入真实配置',async({page})=>{
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 const writes=[];const now='2026-10-03T01:00:00Z'
 const module={key:'eps',label:'EPS 测试模块',provider:'longbridge',pages:['监控标的','小盘候选'],flow:['身份','EPS','本地记录'],auto_capabilities:[],task_names:[],note:'',enabled:true,revision:1,status:'ready_unverified',warnings:[],interfaces:[{key:'eps',label:'EPS 预期',endpoints:['/forecast-eps'],purpose:'盈利预测',required:true,depends_on:[],enabled:true,last_status:'not_recorded'},{key:'anomaly',label:'市场异动',endpoints:['/changes'],purpose:'背景补充',required:false,depends_on:[],enabled:true,last_status:'not_recorded'}]}
 const price={configured:'longbridge,futu',order:['longbridge','futu'],editable:true,sources:[{key:'longbridge',ready:true,reason:'凭据已配置'},{key:'futu',ready:true,reason:'凭据已配置'}],scope:'仅候选 / 监控行情'}
 await page.route('**/api/providers/**',route=>{
  const req=route.request()
  if(req.method()!=='GET'){
   const body=req.postDataJSON();writes.push({path:new URL(req.url()).pathname,body})
   if(req.url().endsWith('/modules/eps')){Object.assign(module,{enabled:body.enabled,revision:module.revision+1,status:body.enabled?'ready_unverified':'disabled'});module.interfaces.forEach(api=>api.enabled=body.interfaces[api.key])}
   if(req.url().endsWith('/price-route')){price.order=[...body.order];price.configured=body.order.join(',')}
   return route.fulfill({json:{data:{saved:true}}})
  }
  return route.fulfill({json:{data:{generated_at:now,window_start:now,time_zone:'Asia/Hong_Kong',providers:[],modules:[module],price_route:price,capabilities:[],tasks:[],trends:[],calls:[],coverage:[],notice:'本地读取'}}})
 })
 await page.goto(`${fixtureURL}/api-management?section=modules`)
 await page.getByText('EPS 测试模块',{exact:true}).click()
 await expect(page.getByRole('switch',{name:'EPS 测试模块 EPS 预期允许调用',exact:true})).toBeDisabled()
 // Element Plus keeps the native switch input at zero size; click its visible wrapper.
 await page.getByRole('switch',{name:'EPS 测试模块允许调用',exact:true}).locator('..').click()
 expect(writes).toHaveLength(0)
 await page.getByRole('button',{name:'保存本模块',exact:true}).click()
 await expect(page.getByRole('dialog')).toContainText('监控标的、小盘候选')
 await page.getByRole('dialog').getByRole('button',{name:'取消',exact:true}).click()
 expect(writes).toHaveLength(0)
 await page.getByRole('button',{name:'保存本模块',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'确定',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(1)
 expect(writes[0].body).toEqual({enabled:false,revision:1,provider:'longbridge',interfaces:{eps:true,anomaly:true},confirm_impact:true})
 await page.getByRole('button',{name:'Futu优先级上移',exact:true}).click()
 expect(writes).toHaveLength(1)
 await page.getByRole('button',{name:'保存行情调用顺序',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'确定',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(2)
 expect(writes[1]).toEqual({path:'/api/providers/price-route',body:{order:['futu','longbridge'],expected:'longbridge,futu'}})
 await expect(page.getByText('当前生效顺序：Futu → Longbridge',{exact:true})).toBeVisible()
})

test('数据源管理只读加载、暂停状态与本地覆盖，不派发外部请求',async({page})=>{
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 const now='2026-10-03T01:00:00Z';let writes=0
 const policy=(provider,paused,budget)=>({provider,paused,daily_budget:budget,daily_used:0,min_interval_ms:1100,created_at:now,budget_date:'2026-10-03'})
 const summary=(provider,paused,budget)=>({policy:policy(provider,paused,budget),credential_configured:false,authorization:'not_configured',requests:0,failures:0,rate_limited:0,average_ms:0,vendor_quota:null})
 await page.route('**/api/providers/**',route=>{if(route.request().method()!=='GET'){writes++;return route.abort()};return route.fulfill({json:{data:{generated_at:now,window_start:'2026-09-27T16:00:00Z',time_zone:'Asia/Hong_Kong',providers:[summary('longbridge',false,0),summary('futu',true,50)],capabilities:[{key:'aggregate',label:'机构合计历史',provider:'futu',auto_enabled:false,issuer_budget:4,ttl_hours:24,metric_scope:'独立报告期口径',implemented:true}],tasks:[],trends:[],calls:[],coverage:[{ticker:'TEST',capability:'aggregate',status:'no_coverage',synced_at:now},{ticker:'TEST',capability:'eps',status:'stale',synced_at:'2026-09-01T00:00:00Z',snapshot_at:'2026-09-01T00:00:00Z',ttl_hours:168},{ticker:'TEST',capability:'analyst',status:'available',synced_at:now,checked_at:now,snapshot_at:'2026-08-01T00:00:00Z',ttl_hours:168}],notice:'本地预算不是供应商配额；页面读取不查询外部 API'}}})})
 await page.goto(`${fixtureURL}/api-management`)
 await expect(page.getByRole('heading',{name:'数据源与 API',exact:true})).toBeVisible()
 await expect(page.getByText('暂停外部调用',{exact:true})).toBeVisible()
 await expect(page.getByText('未知，不推算',{exact:true})).toHaveCount(2)
 await page.getByRole('tab',{name:'覆盖与新鲜度',exact:true}).click()
 await expect(page.getByRole('button',{name:'试查并保存机构合计历史',exact:true})).toBeDisabled()
 await expect(page.getByText('暂无覆盖',{exact:true})).toBeVisible()
 await expect(page.getByText('同步已超期',{exact:true})).toBeVisible()
 await page.getByText('同步已超期',{exact:true}).hover()
 await expect(page.getByRole('tooltip')).toContainText('暂按快照时间判断；超期阈值：7 天')
 await page.getByText('已同步',{exact:true}).hover()
 await expect(page.getByRole('tooltip',{name:/^最近成功查询/})).toContainText('最近成功查询')
 await expect(page.getByRole('tooltip',{name:/^最近成功查询/})).toContainText('数据快照保存')
 await page.getByRole('tab',{name:'调用趋势与审计',exact:true}).click()
 await expect(page.getByText('尚无记录；不把启用前的历史调用量补成零',{exact:true})).toBeVisible()
 expect(writes).toBe(0)
})

test('富途凭据可留空、保存后不自动启用，模块切换需确认且展示真实接口',async({page})=>{
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 const writes=[];const now='2026-10-03T01:00:00Z'
 const credentials={mode:'api_key',algorithm:'Ed25519',app_key_configured:false,private_key_configured:false,oauth_configured:false}
 const api={key:'profile',label:'公司资料',purpose:'公司背景',required:true,depends_on:[],enabled:true,last_status:'not_recorded',endpoints:['/v1/quote/comp-overview']}
 const futuApi={...api,endpoints:['/api/v1.0/quote/{symbol}/company/profile']}
 const module={key:'company',label:'公司资料',provider:'longbridge',pages:['监控标的','小盘候选'],flow:['身份','资料','本地缓存'],auto_capabilities:[],task_names:[],enabled:true,revision:1,status:'ready_unverified',warnings:[],note:'',interfaces:[api],source_options:[{provider:'longbridge',implemented:true,reason:'已接入',interfaces:[api]},{provider:'futu',implemented:true,reason:'已接入',interfaces:[futuApi]}]}
 const summary=provider=>({policy:{provider,paused:true,daily_budget:50,daily_used:0,min_interval_ms:100,created_at:now,budget_date:'2026-10-03'},credential_configured:provider==='futu'&&credentials.app_key_configured,authorization:'not_configured',requests:0,failures:0,rate_limited:0,pending:0,average_ms:0,vendor_quota:null})
 await page.route('**/api/providers/**',route=>{
  const req=route.request(),path=new URL(req.url()).pathname
  if(req.method()==='GET')return route.fulfill({json:{data:path.endsWith('/credentials')?credentials:{generated_at:now,window_start:now,time_zone:'Asia/Hong_Kong',providers:[summary('longbridge'),summary('futu')],modules:[module],capabilities:[],tasks:[],trends:[],calls:[],coverage:[],notice:'本地读取'}}})
  const body=req.postDataJSON();writes.push({path,body})
  if(path.endsWith('/credentials'))Object.assign(credentials,{mode:body.mode,algorithm:body.algorithm,app_key_configured:!!body.app_key,private_key_configured:!!body.private_key})
  if(path.endsWith('/modules/company'))Object.assign(module,{provider:body.provider,revision:module.revision+1,interfaces:[body.provider==='futu'?futuApi:api]})
  return route.fulfill({json:{data:{saved:true}}})
 })
 await page.goto(`${fixtureURL}/api-management`)
 await expect(page.getByRole('heading',{name:'富途凭据（可选）',exact:true})).toBeVisible()
 await page.getByRole('button',{name:'保存富途凭据',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(1)
 expect(writes[0]).toEqual({path:'/api/providers/futu/credentials',body:{mode:'api_key',algorithm:'Ed25519',app_key:'',private_key:''}})
 await page.getByLabel('富途 AppKey',{exact:true}).fill('test-ui-key')
 await page.getByRole('textbox',{name:'富途签名私钥',exact:true}).fill('test-ui-only-placeholder-not-a-real-key')
 await page.getByRole('button',{name:'保存富途凭据',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(2)
 await expect(page.getByRole('textbox',{name:'富途签名私钥',exact:true})).toHaveValue('')
 await expect(page.getByRole('button',{name:'测试只读连接',exact:true}).last()).toBeDisabled()
 await page.getByRole('tab',{name:'行情与接口',exact:true}).click()
 await page.locator('.module-title').getByText('公司资料',{exact:true}).click()
 await page.locator('.module-editor .el-select__wrapper').click()
 await page.getByRole('option',{name:'Futu · 已接入',exact:true}).click()
 await expect(page.getByText('/api/v1.0/quote/{symbol}/company/profile',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'保存本模块',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'取消',exact:true}).click()
 expect(writes).toHaveLength(2)
 await page.getByRole('button',{name:'保存本模块',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'确定',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(3)
 expect(writes[2].body).toMatchObject({provider:'futu',revision:1,confirm_impact:true})
 expect(writes.every(write=>!write.path.endsWith('/probe')&&!write.path.endsWith('/oauth/start'))).toBe(true)
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
  await page.getByRole('button',{name:'刷新 Longbridge 主要机构历史',exact:true}).click()
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

test('集成设置统一入口，旧链接跳转，分区保存不会覆盖凭据或行情顺序', async ({page}) => {
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 const writes=[]
 await page.route('**/api/providers/**',route=>route.fulfill({json:{data:{generated_at:'2026-10-03T01:00:00Z',window_start:'2026-10-03T01:00:00Z',time_zone:'Asia/Hong_Kong',providers:[],modules:[],capabilities:[],tasks:[],trends:[],calls:[],coverage:[],notice:'本地读取'}}}))
 const configs=[{config_key:'discovery.price_provider',config_value:'longbridge,futu'},{config_key:'discovery.longbridge_app_key',config_value:'********'},{config_key:'ui.default_locale',config_value:'zh-CN'},{config_key:'scheduler.timezone',config_value:'Asia/Hong_Kong'}]
 await page.route('**/api/system-configs',route=>{
  if(route.request().method()==='GET')return route.fulfill({json:{data:configs}})
  writes.push({path:'/system-configs',body:route.request().postDataJSON()});return route.fulfill({json:{data:{saved:true}}})
 })
 for(const path of ['ai/providers/config','ai/prompt-templates','telegram/config'])await page.route(`**/api/${path}`,route=>{
  if(route.request().method()==='GET')return route.fulfill({json:{data:[]}})
  writes.push({path,body:route.request().postDataJSON()});return route.fulfill({json:{data:{saved:true}}})
 })
 await page.goto(`${fixtureURL}/configs`)
 await expect(page.getByRole('heading',{name:'系统配置',exact:true})).toBeVisible()
 await expect(page.getByText('数据源与同步',{exact:true})).toHaveCount(0)
 await page.screenshot({path:'test-results/system-settings.png',fullPage:true,animations:'disabled'})
 await expect(page.getByRole('button',{name:'保存当前分类',exact:true})).toBeEnabled()
 await page.getByRole('button',{name:'保存当前分类',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(1)
 expect(writes[0].body.map(item=>item.key).sort()).toEqual(['scheduler.timezone','ui.default_locale'])
 await page.goto(`${fixtureURL}/configs?section=discovery`)
 await expect(page).toHaveURL(/\/api-management\?section=data$/)
 await expect(page.getByRole('heading',{name:'同步策略',exact:true})).toBeVisible()
 await expect(page.getByRole('button',{name:'保存当前分类',exact:true})).toBeEnabled()
 await page.getByRole('button',{name:'保存当前分类',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(2)
 const keys=writes[1].body.map(item=>item.key)
 expect(keys).toContain('sec.user_agent')
 expect(keys).toContain('discovery.longbridge_candidate_research_request_budget')
 expect(keys).not.toContain('discovery.price_provider')
 expect(keys).not.toContain('discovery.longbridge_app_key')
 expect(keys).not.toContain('discovery.twelve_data_api_key')
 await page.getByRole('tab',{name:'连接与凭据',exact:true}).click()
 await expect(page.getByRole('button',{name:'调整行情调用顺序',exact:true})).toBeVisible()
 await expect(page.getByText(/Tiingo|Twelve Data/)).toHaveCount(0)
 await expect(page.getByText('Yahoo Finance · 期货接口地址',{exact:true})).toHaveCount(0)
 await expect(page.getByText('Longbridge → Futu',{exact:true}).first()).toBeVisible()
 await page.screenshot({path:'test-results/integration-connections.png',fullPage:true,animations:'disabled'})
 await expect(page.getByRole('button',{name:'保存当前分类',exact:true})).toBeEnabled()
 await page.getByRole('button',{name:'保存当前分类',exact:true}).click()
 await expect.poll(()=>writes.length).toBe(3)
 expect(writes[2].body.map(item=>item.key)).toContain('discovery.longbridge_app_key')
 expect(writes[2].body.map(item=>item.key)).not.toContain('discovery.price_provider')
 expect(writes[2].body.map(item=>item.key)).not.toContain('sec.user_agent')
 expect(writes[2].body.some(item=>/tiingo|twelve_data|yahoo/.test(item.key))).toBe(false)
 await page.goto(`${fixtureURL}/configs?section=ai`)
 await expect(page).toHaveURL(/\/api-management\?section=ai$/)
 await expect(page.getByRole('heading',{name:'AI 模型',exact:true})).toBeVisible()
 await page.goto(`${fixtureURL}/telegram`)
 await expect(page).toHaveURL(/\/api-management\?section=notifications$/)
 await expect(page.getByRole('heading',{name:'通知与 Telegram',exact:true}).first()).toBeVisible()
})

test('Futu 与 Longbridge 机构刷新独立，缓存提示准确，失败保留已显示数据', async ({page}) => {
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 let futuRequests=0,longbridgeRequests=0
 const point={holder_id:'1',holder_name:'Example Capital',owner_type:'Institution',period:'Q1 2026',holding_date:'',provider_date:'2026-03-31',filing_date:'',percent_of_shares:2,shares_held:1000,source_kind:'detail',source_url:'https://open.longbridge.com/docs/fundamental/fundamental/shareholder-detail',fetched_at:'2026-10-01T00:00:00Z'}
 const history={ticker:'TEST',institutional_holders:[],fund_holders:[{fund_name:'Example ETF',fund_symbol:'ETF',position_ratio:1.5,report_date:'2026-09-30'}],other_holders:[],ownership_history:[point],history_warnings:[],message:'本地记录',futu_aggregate_status:'not_synced',futu_aggregate_history:[]}
 const refreshed={...history,ownership_history:[],futu_aggregate_status:'available',futu_aggregate_synced_at:'2026-10-03T08:00:00Z',futu_aggregate_history:[{ticker:'TEST',period:'2026/Q3',holder_pct:67.347,fetched_at:'2026-10-03T08:00:00Z',source_url:'https://open.futunn.com/api/quote/shareholders/institutional'}]}
 await page.route('**/api/institutional-filings',route=>route.fulfill({json:{data:[]}}))
 await page.route('**/api/discovery/institutional-holdings/TEST',route=>route.fulfill({json:{data:history}}))
 await page.route('**/api/discovery/institutional-holdings/TEST/refresh',route=>{
  longbridgeRequests++;return route.fulfill({json:{data:{research:history,refresh:{warnings:[]}}}})
 })
 await page.route('**/api/providers/futu/ownership/TEST/refresh',route=>{
  futuRequests++
  if(futuRequests===3)return route.fulfill({status:503,json:{message:'Futu 已暂停'}})
  return route.fulfill({json:{data:{research:refreshed,refresh:{cached:futuRequests===2,status:'available',pages:1,points:1}}}})
 })
 await page.goto(`${fixtureURL}/institutional-holdings?ticker=TEST`)
 await expect(page.getByRole('button',{name:'刷新 Futu 机构合计历史',exact:true})).toBeEnabled()
 const futuBlock=page.getByRole('region',{name:'Futu · 机构合计历史',exact:true})
 const longbridgeBlock=page.getByRole('region',{name:'Longbridge · 单家主要机构历史',exact:true})
 await expect(futuBlock.getByRole('button',{name:'刷新 Futu 机构合计历史',exact:true})).toHaveCount(1)
 await expect(futuBlock.getByRole('button',{name:'刷新 Longbridge 主要机构历史',exact:true})).toHaveCount(0)
 await expect(longbridgeBlock.getByRole('button',{name:'刷新 Longbridge 主要机构历史',exact:true})).toHaveCount(1)
 await expect(longbridgeBlock.getByRole('button',{name:'刷新 Futu 机构合计历史',exact:true})).toHaveCount(0)
 await expect(longbridgeBlock).toContainText('Example ETF')
 await expect(futuBlock).not.toContainText('Example ETF')
 expect((await longbridgeBlock.boundingBox()).y).toBeGreaterThan((await futuBlock.boundingBox()).y)
 expect(futuRequests).toBe(0);expect(longbridgeRequests).toBe(0)
 await page.getByRole('button',{name:'刷新 Futu 机构合计历史',exact:true}).click()
 await expect(page.locator('.futu-history')).toContainText('67.35%')
 await expect(page.locator('.futu-history')).toContainText('请求 1 页，保存 1 条报告期记录')
 await expect(page.locator('.history-table')).toContainText('2.00%')
 expect(futuRequests).toBe(1);expect(longbridgeRequests).toBe(0)
 await page.getByRole('button',{name:'刷新 Longbridge 主要机构历史',exact:true}).click()
 await expect.poll(()=>longbridgeRequests).toBe(1)
 await expect(page.locator('.futu-history')).toContainText('67.35%')
 expect(futuRequests).toBe(1)
 await page.getByRole('button',{name:'刷新 Futu 机构合计历史',exact:true}).click()
 await expect(page.locator('.futu-history')).toContainText('已读取 Futu 24 小时缓存')
 await page.getByRole('button',{name:'刷新 Futu 机构合计历史',exact:true}).click()
 await expect(page.locator('.futu-history')).toContainText('Futu 刷新失败，已有数据保留：Futu 已暂停')
 await expect(page.locator('.futu-history')).toContainText('67.35%')
 expect(futuRequests).toBe(3);expect(longbridgeRequests).toBe(1)
 await expect(page.locator('.el-message')).toHaveCount(0)
 await page.setViewportSize({width:1440,height:1600})
 await futuBlock.scrollIntoViewIfNeeded()
 await page.screenshot({path:'test-results/ownership-provider-desktop.png',fullPage:true,animations:'disabled'})
 await page.setViewportSize({width:390,height:844})
 await futuBlock.scrollIntoViewIfNeeded()
 await page.screenshot({path:'test-results/ownership-provider-futu-mobile.png',fullPage:true,animations:'disabled'})
 await longbridgeBlock.scrollIntoViewIfNeeded()
 const block=await longbridgeBlock.boundingBox(),button=await longbridgeBlock.getByRole('button',{name:'刷新 Longbridge 主要机构历史',exact:true}).boundingBox()
 expect(button.x).toBeGreaterThanOrEqual(block.x)
 expect(button.x+button.width).toBeLessThanOrEqual(block.x+block.width)
 await page.screenshot({path:'test-results/ownership-provider-mobile.png',fullPage:true,animations:'disabled'})
})

test('期货页只读取本地富途数据，手动刷新独立调用且没有 Yahoo 设置',async({page})=>{
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 let refreshes=0
 await page.route('**/api/us-futures**',route=>{
  if(route.request().method()==='POST'){refreshes++;return route.fulfill({json:{data:{symbols_requested:10,symbols_updated:0,bars_saved:0,warnings:['富途暂无覆盖']}}})}
  return route.fulfill({json:{data:{source:'futu',futures:[]}}})
 })
 await page.goto(`${fixtureURL}/us-futures`)
 await expect(page.getByRole('heading',{name:'美股期货',exact:true})).toBeVisible()
 await expect(page.getByText(/数据源：Futu 官方 API 主连合约/)).toBeVisible()
 await expect(page.getByText(/授权、模块开关、全局暂停与每日请求预算/)).toBeVisible()
 expect(refreshes).toBe(0)
 await page.getByRole('button',{name:'刷新视图',exact:true}).click()
 expect(refreshes).toBe(0)
 await page.getByRole('button',{name:'刷新 Futu 期货日线',exact:true}).click()
 await expect.poll(()=>refreshes).toBe(1)
 await expect(page.getByText(/Yahoo/)).toHaveCount(0)
})

test('期权页默认展示按同步时间倒序的本地最新快照，点击详情不会调用外部接口',async({page})=>{
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 const rows=[
  {id:3,ticker:'NEW',provider:'longbridge',observed_date:'2026-10-03',status:'available',call_volume:0,put_volume:120,short_ratio_pct:2.3,fetched_at:'2026-10-03T02:00:00Z',anomalies:[]},
  {id:2,ticker:'MID',provider:'longbridge',observed_date:'2026-10-03',status:'unavailable',fetched_at:'2026-10-03T01:00:00Z',anomalies:[]},
  {id:1,ticker:'OLD',provider:'longbridge',observed_date:'2026-10-02',status:'partial',short_ratio_pct:1.2,fetched_at:'2026-10-02T01:00:00Z',anomalies:[]}
 ]
 let writes=0,listReads=0
 await page.route('**/api/discovery/options**',route=>{
  const req=route.request(),url=new URL(req.url())
  if(req.method()==='POST'){writes++;return route.fulfill({json:{data:{research:{latest:rows[0],history:[rows[0]],message:'完整快照'},refresh:{message:'已刷新'}}}})}
  if(url.pathname==='/api/discovery/options'){listReads++;return route.fulfill({json:{data:{items:rows,total:3,page:1,page_size:10,summary:{total:3,option_covered:1,short_covered:2,last_fetched_at:rows[0].fetched_at}}}})}
  const row=rows.find(r=>url.pathname.endsWith('/'+r.ticker))
  return route.fulfill({json:{data:{latest:row,history:row?[row]:[],message:'本地详情'}}})
 })
 await page.goto(`${fixtureURL}/option-research`)
 await expect(page.getByText('已同步 3 个标的',{exact:true})).toBeVisible()
 const bodyRows=page.locator('.snapshot-list .el-table__body tbody tr')
 await expect(bodyRows).toHaveCount(3)
 await expect(bodyRows.nth(0)).toContainText('NEW')
 await expect(bodyRows.nth(1)).toContainText('MID')
 await expect(bodyRows.nth(2)).toContainText('OLD')
 await expect(page.getByText('1 个标的可用',{exact:true})).toBeVisible()
 await expect(page.getByText('2 个标的可用',{exact:true})).toBeVisible()
 expect(writes).toBe(0)
 await page.getByRole('button',{name:'刷新列表',exact:true}).click()
 await expect.poll(()=>listReads).toBe(2)
 expect(writes).toBe(0)
 await page.getByRole('button',{name:'NEW',exact:true}).click()
 await expect(page.getByRole('heading',{name:'NEW · 期权与多空详情',exact:true})).toBeVisible()
 await expect(page.getByText('本地详情',{exact:true})).toBeVisible()
 expect(writes).toBe(0)
 await page.getByRole('button',{name:'返回列表',exact:true}).click()
 await expect(page.getByRole('heading',{name:'NEW · 期权与多空详情',exact:true})).toHaveCount(0)
 await expect(page.getByText('已同步 3 个标的',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'刷新 Longbridge 数据',exact:true}).click()
 await expect.poll(()=>writes).toBe(1)
 await expect.poll(()=>listReads).toBe(3)
})

test('期权列表区分读取失败与没有本地数据，不误报未覆盖',async({page})=>{
 await page.goto(`${fixtureURL}/__test/reset?scenario=success`)
 let failed=true
 await page.route('**/api/discovery/options**',route=>{
  if(failed)return route.fulfill({status:500,json:{message:'列表数据库读取失败'}})
  return route.fulfill({json:{data:{items:[],total:0,page:1,page_size:10,summary:{total:0,option_covered:0,short_covered:0}}}})
 })
 await page.goto(`${fixtureURL}/option-research`)
 await expect(page.getByText('列表数据库读取失败',{exact:true})).toBeVisible()
 await expect(page.getByText('读取失败',{exact:true})).toBeVisible()
 await expect(page.getByText('待查询',{exact:true})).toHaveCount(2)
 await expect(page.getByText('未覆盖',{exact:true})).toHaveCount(0)
 failed=false
 await page.getByRole('button',{name:'刷新列表',exact:true}).click()
 await expect(page.getByText('已同步 0 个标的',{exact:true})).toBeVisible()
 await expect(page.getByText('暂无本地快照；输入标的后可刷新 Longbridge 数据。',{exact:true})).toBeVisible()
})

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

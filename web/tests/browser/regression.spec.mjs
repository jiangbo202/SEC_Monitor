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

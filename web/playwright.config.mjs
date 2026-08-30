import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir:'./tests/browser',
  testMatch:'**/*.spec.mjs',
  workers:1,
  fullyParallel:false,
  timeout:60000,
  forbidOnly:!!process.env.CI,
  retries:process.env.CI?1:0,
  reporter:[['list'],['html',{open:'never'}]],
  use:{browserName:'chromium',viewport:{width:1440,height:1000},trace:'retain-on-failure',screenshot:'only-on-failure'},
  webServer:{command:'go run ../internal/testfixture/browser',env:{GOTOOLCHAIN:'auto'},url:'http://127.0.0.1:19090/__test/status',reuseExistingServer:false,timeout:120000},
})

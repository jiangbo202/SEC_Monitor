import {describe,it,expect} from 'vitest'
import {moduleDraft,moduleDisables,moduleValidation,moveAPIPriceSource} from './apiModules'
import type {APIModuleView} from '@/api/providers'

const module:APIModuleView={
 key:'test',label:'测试研究',provider:'longbridge',pages:['监控标的'],flow:['身份','列表','历史'],
 auto_capabilities:[],task_names:[],note:'',enabled:true,revision:3,status:'ready_unverified',warnings:[],
 interfaces:[
  {key:'list',label:'列表',required:true,depends_on:[],endpoints:['/list'],purpose:'身份识别',enabled:true,last_status:'not_recorded'},
  {key:'history',label:'历史',required:false,depends_on:['list'],endpoints:['/history'],purpose:'历史补充',enabled:true,last_status:'success'}
 ]
}
describe('API 模块配置草稿',()=>{
 it('编辑草稿不会修改已保存配置；只有关闭才需要影响确认',()=>{
  const draft=moduleDraft(module)
  expect(moduleDisables(module,draft)).toBe(false)
  draft.interfaces.history=false
  expect(module.interfaces[1]!.enabled).toBe(true)
  expect(moduleDisables(module,draft)).toBe(true)
  expect(moduleValidation(module,draft)).toBe('')
 })
 it('验证必需接口和固定依赖，即使模块关闭也不能保存坏依赖',()=>{
  const draft=moduleDraft(module);draft.interfaces.list=false
  expect(moduleValidation(module,draft)).toContain('必需')
  draft.enabled=false
  expect(moduleValidation(module,draft)).toContain('前置')
  draft.interfaces.history=false
  expect(moduleValidation(module,draft)).toBe('')
  draft.enabled=true
  expect(moduleValidation(module,draft)).toContain('必需')
 })
 it('仅移动已选行情源；不会原地修改列表或越界',()=>{
  const order=['longbridge','futu']
  expect(moveAPIPriceSource(order,1,-1)).toEqual(['futu','longbridge'])
  expect(moveAPIPriceSource(order,0,-1)).toEqual(order)
  expect(moveAPIPriceSource(order,1,1)).toEqual(order)
  expect(order).toEqual(['longbridge','futu'])
 })
})

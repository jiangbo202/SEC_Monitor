import { describe, expect, it } from 'vitest'
import {apiBudgetPercent,apiSuccessRate,apiStateLabel} from './apiManagement'
describe('API management semantics',()=>{
 it('local safeguards are capped and unlimited does not imply vendor quota',()=>{expect(apiBudgetPercent(100,50)).toBe(100);expect(apiBudgetPercent(10,0)).toBe(0)})
 it('unknown and pending are not successful coverage',()=>{expect(apiSuccessRate(0,0)).toBeNull();expect(apiSuccessRate(5,1,1)).toBe(75);expect(apiStateLabel('no_coverage')).toBe('暂无覆盖');expect(apiStateLabel('stale')).toBe('同步已超期')})
})

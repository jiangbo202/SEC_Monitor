import {isAxiosError} from 'axios'
export function apiErrorMessage(error:unknown){if(isAxiosError(error)&&typeof error.response?.data?.message==='string')return error.response.data.message;return error instanceof Error?error.message:'操作失败'}
const stateLabels:Record<string,string> = {api_key_configured_unverified:'签名凭据已配置，待验证',not_synced:'未同步',available:'已同步',partial:'部分覆盖',stale:'同步已超期',no_coverage:'暂无覆盖',success:'请求成功',failed:'请求失败',started:'进行中 / 未完成',paused:'已暂停',not_configured:'凭据未配置',configured_unverified:'凭据已配置，待验证',quote_read_authorized:'行情只读已授权',rate_limited:'提供方限流',authorization:'授权 / 权限',timeout:'超时',request_failed:'请求异常'}
Object.assign(stateLabels,{not_recorded:'尚无请求记录',sdk_internal:'SDK 内部',background:'后台',manual:'手动',page:'页面',degraded:'降级完成',skipped:'已跳过',running:'运行中',interrupted:'已中断'})
export const apiStateLabel = (state?:string) => stateLabels[state || ''] || state || '未记录'
export function apiBudgetPercent(used:number,budget:number){return budget>0?Math.min(100,Math.max(0,used/budget*100)):0}
export function apiSuccessRate(requests:number,failures:number,pending=0){const complete=requests-pending;return complete>0?Math.max(0,(complete-failures)/complete*100):null}

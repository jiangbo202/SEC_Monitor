import type { APIModuleView } from '@/api/providers'
export interface ModuleDraft {enabled:boolean;interfaces:Record<string,boolean>;revision:number;provider?:string}
export function moduleDraft(module:APIModuleView):ModuleDraft {return {enabled:module.enabled,revision:module.revision,provider:module.provider,interfaces:Object.fromEntries(module.interfaces.map(api=>[api.key,api.enabled]))}}
export function moduleDisables(module:APIModuleView,draft:ModuleDraft){return (draft.provider!==undefined&&draft.provider!==module.provider)||(module.enabled&&!draft.enabled)||module.interfaces.some(api=>api.enabled&&!draft.interfaces[api.key])}
export function moduleValidation(module:APIModuleView,draft:ModuleDraft):string {
 if(draft.provider&&module.source_options&&!module.source_options.some(source=>source.provider===draft.provider&&source.implemented))return '该供应商尚未适配此功能，不能启用'
 for(const api of module.interfaces){if(draft.enabled&&api.required&&!draft.interfaces[api.key])return `请恢复必需接口：${api.label}`;if(draft.interfaces[api.key]&&(api.depends_on||[]).some(key=>!draft.interfaces[key]))return `${api.label}缺少前置接口`}
 return ''
}
export function moveAPIPriceSource(order:string[],index:number,direction:-1|1){const next=[...order],target=index+direction;if(index<0||index>=next.length||target<0||target>=next.length)return next;[next[index],next[target]]=[next[target]!,next[index]!];return next}

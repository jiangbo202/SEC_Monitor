<template>
  <el-card shadow="never" class="thesis-card">
    <template #header><div class="thesis-head"><strong>研究观点 · {{ ticker }}</strong><div><el-tag v-if="current" :type="current.status === 'active' ? 'success' : current.status === 'invalidated' ? 'danger' : 'info'" effect="plain">{{ statusLabel(current.status) }} · v{{ current.version }}</el-tag><el-button link :disabled="editing || loading" @click="load">刷新观点</el-button><el-button :disabled="loading || !!loadError" @click="beginEdit">{{ current ? '编辑 / 复核' : '建立研究观点' }}</el-button></div></div></template>
    <p class="muted">人工维护的研究判断，独立于系统评分与交易台账；新证据只提示复核，不自动改变观点或交易状态。</p>
    <p v-if="loading" class="muted" role="status">正在读取观点与本地证据…</p>
    <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" /><el-button v-if="loadError" @click="load">重试读取观点</el-button>
    <el-alert v-if="due || newCount" class="thesis-gap" :title="`${due ? '已到复核时间。' : ''}${newCount ? `上次复核后有 ${newCount} 条新入库证据。` : ''}请核对观点是否仍成立。`" type="warning" :closable="false" />
    <el-alert v-if="sourceError" class="thesis-gap" :title="sourceError" type="warning" :closable="false" />
    <template v-if="current && !editing && !loading">
      <div class="thesis-grid thesis-gap">
        <div><small>研究理由</small><p>{{ current.rationale || '草稿待补充' }}</p></div>
        <div><small>失效条件</small><p>{{ current.invalidation || '尚未填写' }}</p></div>
        <div><small>下次验证事项</small><p>{{ current.next_check || '尚未填写' }}</p><small v-if="current.next_review_at">{{ date(current.next_review_at) }}</small></div>
        <div><small>本次复核结论</small><p>{{ current.review_note || '尚未复核' }}</p><small>最近复核：{{ date(current.reviewed_at) }}</small></div>
      </div>
      <div class="thesis-evidence thesis-gap"><small>关联证据（保存时快照）</small><span v-if="!current.evidence.length">暂无证据</span><el-link v-for="item in current.evidence" :key="key(item)" :href="item.url || undefined" target="_blank" rel="noopener noreferrer" type="primary" :title="item.summary">{{ item.label }} #{{ item.id }}</el-link></div>
    </template>
    <p v-else-if="!current && !editing && !loading && !loadError" class="muted">尚未建立观点。补充研究理由、关联证据和失效条件，再设置下次复核时间。</p>
    <el-form v-if="editing" label-position="top" class="thesis-gap" @submit.prevent="save">
      <div class="thesis-grid">
        <el-form-item label="研究理由"><el-input v-model="form.rationale" aria-label="研究理由" type="textarea" :rows="3" maxlength="10000" /></el-form-item>
        <el-form-item label="失效条件"><el-input v-model="form.invalidation" aria-label="失效条件" type="textarea" :rows="3" maxlength="10000" /></el-form-item>
        <el-form-item label="下次验证事项"><el-input v-model="form.next_check" aria-label="下次验证事项" type="textarea" :rows="2" maxlength="10000" /></el-form-item>
        <el-form-item label="下次复核时间（本机时区）"><input v-model="reviewDate" aria-label="下次复核时间" type="datetime-local" class="review-date" /></el-form-item>
      </div>
      <el-form-item label="本次复核结论 / 修订理由"><el-input v-model="form.review_note" aria-label="本次复核结论" type="textarea" :rows="2" placeholder="哪些证据支持或推翻了原观点？为什么维持、修订或结束跟踪？" maxlength="10000" /></el-form-item>
      <el-form-item label="观点状态"><el-radio-group v-model="form.status"><el-radio-button value="draft">草稿</el-radio-button><el-radio-button value="active" :disabled="closed">跟踪中</el-radio-button><el-radio-button value="invalidated">已失效</el-radio-button><el-radio-button value="archived">已归档</el-radio-button></el-radio-group><small v-if="closed" class="muted">已结束观点须先保存为草稿，再重新跟踪。</small></el-form-item>
      <el-form-item label="关联本地证据（每类最近 20 条；最多选 30 条）">
        <div class="evidence-picker"><label v-for="item in evidenceOptions" :key="key(item)"><input v-model="selected" type="checkbox" :value="key(item)" :aria-label="`${item.label} #${item.id}`" /><span><b>{{ item.label }} #{{ item.id }}</b><small>{{ item.summary || '打开来源核对详情' }}</small></span></label><span v-if="!evidenceOptions.length" class="muted">暂无可关联证据，可先保存草稿。</span></div>
      </el-form-item>
      <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" class="thesis-gap" />
      <div class="thesis-actions"><el-button type="primary" :loading="saving" :disabled="conflict" @click="save">保存观点</el-button><el-button :disabled="saving" @click="cancel">取消编辑</el-button><el-button v-if="conflict" @click="reloadConflict">重新加载最新版本</el-button><span class="muted">每次保存均保留完整修订历史。</span></div>
    </el-form>
    <el-collapse v-if="revisions.length" class="thesis-gap"><el-collapse-item title="观点修订历史（最近 50 版）" name="history"><article v-for="revision in revisions" :key="revision.version" class="revision"><strong>v{{ revision.version }} · {{ statusLabel(revision.snapshot.status) }} · {{ date(revision.created_at) }}</strong><p>研究理由：{{ revision.snapshot.rationale || '—' }}</p><p>失效条件：{{ revision.snapshot.invalidation || '—' }}</p><p>验证事项：{{ revision.snapshot.next_check || '—' }} · {{ date(revision.snapshot.next_review_at) }}</p><p>复核结论：{{ revision.snapshot.review_note || '—' }}</p><details v-if="revision.snapshot.evidence.length"><summary>证据快照（{{ revision.snapshot.evidence.length }}）</summary><div v-for="item in revision.snapshot.evidence" :key="key(item)"><el-link :href="item.url || undefined" target="_blank" rel="noopener noreferrer">{{ item.label }} #{{ item.id }}</el-link><p>{{ item.summary }}</p><small>校验 {{ item.sha256 }}</small></div></details></article></el-collapse-item></el-collapse>
  </el-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
interface Evidence {kind:string;id:number;label:string;url:string;summary:string;sha256:string;recorded_at:string}
interface Thesis {ticker:string;version:number;status:string;rationale:string;invalidation:string;next_check:string;next_review_at:string|null;review_note:string;reviewed_at:string|null;evidence:Evidence[]}
interface Revision {version:number;created_at:string;snapshot:Thesis}
const props=defineProps<{ticker:string}>();const emit=defineEmits<{saved:[]}>()
const current=ref<Thesis|null>(null);const revisions=ref<Revision[]>([]);const sources=ref<Evidence[]>([])
const loading=ref(false);const saving=ref(false);const editing=ref(false);const due=ref(false);const newCount=ref(0)
const loadError=ref('');const sourceError=ref('');const saveError=ref('');const conflict=ref(false)
const empty=()=>({version:0,status:'draft',rationale:'',invalidation:'',next_check:'',review_note:''})
const form=ref(empty());const selected=ref<string[]>([]);const reviewDate=ref('')
const key=(item:Evidence)=>`${item.kind}:${item.id}`
const closed=computed(()=>current.value?.status==='invalidated'||current.value?.status==='archived')
const evidenceOptions=computed(()=>[...new Map([...(current.value?.evidence||[]),...sources.value].map(item=>[key(item),item])).values()])
const statusLabel=(status:string)=>({draft:'草稿',active:'跟踪中',invalidated:'已失效',archived:'已归档'}[status]||status)
const date=(value:string|null)=>value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'未设置'
async function load(){
  loading.value=true;loadError.value='';sourceError.value=''
  const path=`/research-theses/${encodeURIComponent(props.ticker)}`
  const [detail,evidence]=await Promise.allSettled([apiClient.get(path),apiClient.get(`${path}/sources`)])
  if(detail.status==='fulfilled'){current.value=detail.value.data.data.thesis;revisions.value=detail.value.data.data.revisions;due.value=detail.value.data.data.review_due}
  else loadError.value='研究观点读取失败，未将失败当作“没有观点”。请重试。'
  if(evidence.status==='fulfilled'){sources.value=evidence.value.data.data.items;newCount.value=evidence.value.data.data.new_count;if(evidence.value.data.data.unavailable.length)sourceError.value='部分证据来源读取失败，新证据数量可能不完整；已保存证据仍保留。'}
  else sourceError.value='本地证据读取失败，可保留已有证据或保存草稿后重试。'
  loading.value=false
}
function beginEdit(){if(editing.value)return;form.value=current.value?{...current.value,review_note:''}:empty();selected.value=(current.value?.evidence||[]).map(key);reviewDate.value=current.value?.next_review_at?localDate(current.value.next_review_at):'';saveError.value='';conflict.value=false;editing.value=true}
function localDate(value:string){const d=new Date(value);return new Date(d.getTime()-d.getTimezoneOffset()*60000).toISOString().slice(0,16)}
async function confirmDiscard(){if(!editing.value)return true;try{await ElMessageBox.confirm('尚未保存的观点修改将丢失，是否继续？','离开编辑',{confirmButtonText:'放弃修改',cancelButtonText:'继续编辑',type:'warning'});return true}catch{return false}}
async function cancel(){if(await confirmDiscard())editing.value=false}
async function reloadConflict(){if(await confirmDiscard()){editing.value=false;await load();if(!loadError.value)beginEdit()}}
onBeforeRouteLeave(()=>saving.value?false:confirmDiscard())
onBeforeRouteUpdate((to,from)=>to.query.ticker===from.query.ticker?true:saving.value?false:confirmDiscard())
async function save(){
  saving.value=true;saveError.value=''
  try{
    const response=await apiClient.put(`/research-theses/${encodeURIComponent(props.ticker)}`,{...form.value,next_review_at:reviewDate.value?new Date(reviewDate.value).toISOString():null,evidence:evidenceOptions.value.filter(item=>selected.value.includes(key(item))).map(({kind,id})=>({kind,id}))})
    current.value=response.data.data;editing.value=false;ElMessage.success('观点已保存，修订历史已记录');emit('saved');await load()
  }catch(error:any){conflict.value=error?.response?.status===409;saveError.value=error?.response?.data?.message||'保存失败，请保留当前输入后重试。'}finally{saving.value=false}
}
onMounted(load)
</script>

<style scoped>
.thesis-head,.thesis-head>div,.thesis-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.thesis-head{justify-content:space-between}.muted,small{color:var(--el-text-color-secondary);font-size:12px}.thesis-gap{margin-top:12px}.thesis-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.thesis-grid p,.revision p{white-space:pre-wrap;overflow-wrap:anywhere;margin:5px 0 10px}.thesis-grid>div{min-width:0}.thesis-evidence{display:flex;align-items:center;flex-wrap:wrap;gap:10px}.review-date{box-sizing:border-box;width:100%;min-height:30px;border:1px solid var(--el-border-color);border-radius:4px;padding:4px 10px;background:var(--el-bg-color);color:var(--el-text-color-primary)}.evidence-picker{width:100%;max-height:210px;overflow:auto;border:1px solid var(--el-border-color-lighter);padding:8px;border-radius:4px}.evidence-picker label{display:flex;align-items:flex-start;gap:8px;margin:6px 0;line-height:1.5}.evidence-picker small{display:block;overflow-wrap:anywhere}.thesis-actions{margin-top:12px}.revision{padding:10px 0;border-bottom:1px solid var(--el-border-color-lighter);overflow-wrap:anywhere}.revision details{margin:6px 0}.thesis-card :deep(.el-form-item){margin-bottom:8px}@media(max-width:700px){.thesis-grid{grid-template-columns:1fr}}
</style>

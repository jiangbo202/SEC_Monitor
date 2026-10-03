<template>
 <section v-loading="loading">
  <h3>富途凭据（可选）</h3>
  <p class="muted">不配置也可正常使用其他数据源。AppKey 在富途开发者中心创建，上传公钥；这里只保存用于签名的私钥，没有 Longbridge 式 AppSecret。保存不启用调用或任务。</p>
  <el-alert v-if="error" type="error" :closable="false" :title="error" />
  <el-form label-position="top" autocomplete="off">
   <el-form-item label="认证方式"><el-select v-model="draft.mode" aria-label="富途认证方式"><el-option label="手动 AppKey + 签名私钥" value="api_key" /><el-option label="OAuth 行情只读授权" value="oauth" /></el-select></el-form-item>
   <template v-if="draft.mode==='api_key'">
    <el-form-item label="AppKey（留空保留已有值）"><el-input v-model="draft.app_key" type="password" show-password autocomplete="new-password" :placeholder="state?.app_key_configured?'已配置（不会回显）':'未配置，可留空'" aria-label="富途 AppKey" /></el-form-item>
    <el-form-item label="签名算法"><el-select v-model="draft.algorithm" aria-label="富途签名算法"><el-option label="Ed25519" value="Ed25519" /><el-option label="RSA-SHA256（至少 2048 位）" value="RSA-SHA256" /></el-select></el-form-item>
    <el-form-item label="PEM 签名私钥（留空保留已有值，输入时可见）"><el-input v-model="draft.private_key" type="textarea" :rows="4" autocomplete="off" :placeholder="state?.private_key_configured?'私钥已加密保存（不会回显）':'粘贴完整、未加密的 PEM 私钥'" aria-label="富途签名私钥" /></el-form-item>
   </template>
   <p v-else class="muted">OAuth {{ state?.oauth_configured?'已有授权':'尚未授权' }}。先保存认证方式，再使用卡片中的“开始只读授权”；只申请 quote:read。</p>
   <el-space wrap><el-button type="primary" :loading="saving" @click="save">保存富途凭据</el-button><el-button :disabled="saving" @click="load">恢复已保存状态</el-button><el-button v-if="state?.app_key_configured||state?.private_key_configured" type="danger" plain :disabled="saving" @click="clear">清除手动凭据</el-button></el-space>
  </el-form>
  <p class="muted">凭据加密落库，不出现在接口返回或审计中。手动密钥自身权限由富途管理，本项目仍只允许已接入的行情只读接口，不提供交易入口。</p>
 </section>
</template>
<script setup lang="ts">
import {onMounted,reactive,ref} from 'vue'
import {ElMessage,ElMessageBox} from 'element-plus'
import {apiClient} from '@/api/client'
import type {ApiResponse} from '@/api/types'
import {apiErrorMessage} from '@/utils/apiManagement'
interface Credentials {mode:string;algorithm:string;app_key_configured:boolean;private_key_configured:boolean;oauth_configured:boolean}
const emit=defineEmits<{saved:[]}>(),state=ref<Credentials>(),loading=ref(false),saving=ref(false),error=ref('')
const draft=reactive({mode:'api_key',algorithm:'Ed25519',app_key:'',private_key:''})
async function load(){loading.value=true;error.value='';try{const response=await apiClient.get<ApiResponse<Credentials>>('/providers/futu/credentials');state.value=response.data.data;draft.mode=state.value.mode;draft.algorithm=state.value.algorithm;draft.app_key='';draft.private_key=''}catch(err){error.value=apiErrorMessage(err)}finally{loading.value=false}}
async function save(){if(saving.value)return;saving.value=true;error.value='';try{await apiClient.put('/providers/futu/credentials',{...draft});draft.private_key='';draft.app_key='';ElMessage.success('凭据已保存；未自动启用调用或任务');await load();emit('saved')}catch(err){error.value=apiErrorMessage(err)}finally{saving.value=false}}
async function clear(){try{await ElMessageBox.confirm('清除 AppKey / 私钥并暂停富途调用？OAuth 凭据与本地历史保留，不会自动改用 OAuth。','清除手动凭据',{confirmButtonText:'清除',cancelButtonText:'取消',type:'warning'})}catch{return}saving.value=true;try{await apiClient.put('/providers/futu/credentials',{mode:'api_key',algorithm:draft.algorithm,clear:true});draft.private_key='';draft.app_key='';await load();emit('saved')}catch(err){error.value=apiErrorMessage(err)}finally{saving.value=false}}
onMounted(load)
</script>
<style scoped>.muted{font-size:12px;line-height:1.7;color:var(--el-text-color-secondary)}section{margin-top:18px;border-top:1px solid var(--el-border-color-light);padding-top:12px}</style>

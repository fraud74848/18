<template>
  <div class="page-tabs" role="tablist" aria-label="已打开页面">
    <div v-for="tab in tabs" :key="tab.path" class="page-tab" :class="{active: tab.path===activePath}">
      <button role="tab" :aria-selected="tab.path===activePath" @click="router.push(tab.fullPath)">{{tab.title}}</button>
      <button :disabled="tabs.length<2" class="tab-close" :aria-label="`关闭${tab.title}`" @click="close(tab.path)">×</button>
    </div>
    <el-dropdown class="tab-actions" trigger="click" placement="bottom-end" @command="onCommand">
      <button class="tab-menu" aria-label="标签页操作" title="标签页操作"><el-icon :size="16"><ArrowDown /></el-icon></button>
      <template #dropdown><el-dropdown-menu>
        <el-dropdown-item command="refresh" :icon="Refresh">刷新</el-dropdown-item>
        <el-dropdown-item command="current" :icon="Close" :disabled="tabs.length<2">关闭当前</el-dropdown-item>
        <el-dropdown-item command="others" :icon="CircleClose" :disabled="tabs.length<2">关闭其他</el-dropdown-item>
        <el-dropdown-item command="all" :icon="Delete" divided :disabled="!tabs.length">全部关闭</el-dropdown-item>
      </el-dropdown-menu></template>
    </el-dropdown>
  </div>
  <main><router-view v-slot="{ Component, route: pageRoute }">
    <keep-alive :include="cachedNames"><component :is="Component" :key="canonical(pageRoute)+':'+user.permissions.join(',')+':'+(refreshKeys[canonical(pageRoute)]||0)" /></keep-alive>
  </router-view></main>
</template>
<script setup lang="ts">
import {computed,ref,watch} from 'vue';
import {useRoute,useRouter,type RouteLocationNormalizedLoaded} from 'vue-router';
import {useUserStore,canOpenPage} from '../store/user';
import {ArrowDown,Refresh,Close,CircleClose,Delete} from '@element-plus/icons-vue';
const route=useRoute(),router=useRouter(),user=useUserStore();
const pages:Record<string,[string,string]>={
 '/roles':['角色管理','Accounts'],'/password':['修改密码','Accounts'],'/memory':['清理内存','Accounts'],
 '/':['首页概览','Overview'],'/accounts':['子账号管理','Accounts'],
 '/telegram':['打卡规则','TelegramAttendance'],'/telegram-records':['打卡记录','TelegramRecords'],
 '/telegram-punch':['后台打卡','TelegramActivityPunch'],'/attendance':['考勤月历','AttendanceRecords'],
 '/attendance-review':['待确认审批','AttendanceReview'],'/penalty-details':['罚款详情','PenaltyDetails'],
 '/attendance/performance':['员工绩效','PerformanceReview']
};
type PageTab={path:string;fullPath:string;title:string};
const tabs=ref<PageTab[]>([]);
function canonical(r:RouteLocationNormalizedLoaded){const record=r.matched[r.matched.length-1];return record?.aliasOf?.path || r.path}
const activePath=computed(()=>canonical(route));
const cachedNames=computed(()=>tabs.value.map(t=>pages[t.path]?.[1]).filter(name=>!!name && name!=='Accounts'));
function allowed(path:string){return !!pages[path] && canOpenPage(path)}
watch(()=>user.permissions,()=>{tabs.value=tabs.value.filter(t=>allowed(t.path))},{deep:true});
watch(()=>user.userId,()=>{
 tabs.value=[];
 try{const saved=JSON.parse(sessionStorage.getItem('attendance-tabs:'+user.userId)||'[]');if(Array.isArray(saved))tabs.value=saved.filter(t=>typeof t.path==='string'&&allowed(t.path)).map(t=>({path:t.path,fullPath:t.path,title:pages[t.path][0]}));}catch{}
},{immediate:true});
watch(()=>[route.fullPath,user.userId],()=>{
 const path=canonical(route);if(!allowed(path))return;
 const tab=tabs.value.find(t=>t.path===path);
 if(tab)tab.fullPath=route.fullPath;else tabs.value.push({path,fullPath:route.fullPath,title:pages[path][0]});
},{immediate:true});
watch(tabs,t=>{try{sessionStorage.setItem('attendance-tabs:'+user.userId,JSON.stringify(t.map(x=>({path:x.path}))));}catch{}},{deep:true});
async function close(path:string){
 if(tabs.value.length<2)return;
 const index=tabs.value.findIndex(t=>t.path===path);
 if(path===activePath.value){const next=tabs.value[index-1]||tabs.value[index+1];await router.push(next.fullPath);}
 tabs.value=tabs.value.filter(t=>t.path!==path);
}
function closeOthers(){tabs.value=tabs.value.filter(t=>t.path===activePath.value)}
const refreshKeys=ref<Record<string,number>>({});
function refreshCurrent(){const p=activePath.value;refreshKeys.value={...refreshKeys.value,[p]:(refreshKeys.value[p]||0)+1}}
async function closeAll(){
 tabs.value=[];
 const home=['/','/password'].find(p=>canOpenPage(p))||'/password';
 if(canonical(route)===home){const t=pages[home];tabs.value=[{path:home,fullPath:home,title:t[0]}];refreshCurrent()}
 else await router.push(home);
}
function onCommand(cmd:string){
 if(cmd==='refresh')refreshCurrent();
 else if(cmd==='current')void close(activePath.value);
 else if(cmd==='others')closeOthers();
 else if(cmd==='all')void closeAll();
}
</script>
<style scoped>
.page-tabs{display:flex;gap:6px;align-items:center;padding:0 24px;background:#fff;border-bottom:1px solid #e3e9eb;overflow-x:auto;height:var(--workspace-bar-height);position:sticky;top:var(--workspace-bar-height);z-index:99}
.page-tab{display:flex;align-items:center;flex-shrink:0;border:1px solid #e3e9eb;border-radius:8px;background:#f5f7fa}
.page-tab button{border:0;background:transparent;color:#63747a;padding:10px 12px;white-space:nowrap;font-size:13px}
.page-tab.active{background:#e8f3ed;border-color:#b9d9ca;border-bottom-color:#e8f3ed}.page-tab.active button{color:#205c50}
.page-tab .tab-close{padding:8px;font-size:18px;opacity:0;pointer-events:none;width:30px;flex:0 0 30px;transition:opacity .15s}.page-tab:hover .tab-close,.page-tab.active .tab-close,.page-tab:focus-within .tab-close{opacity:1;pointer-events:auto}.tab-close:hover{background:#dbe8e1;border-radius:6px}
.tab-actions{margin-left:auto;flex-shrink:0;position:sticky;right:0;background:#fff}.tab-menu{display:flex;align-items:center;justify-content:center;width:32px;height:32px;border:1px solid #e3e9eb;border-radius:8px;background:#f5f7fa;color:#63747a;outline:none}.tab-menu:hover{background:#e8f3ed;color:#205c50;border-color:#b9d9ca}
@media(max-width:900px){.page-tabs{padding:0 12px}}
</style>

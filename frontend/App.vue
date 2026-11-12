<template>
  <router-view v-if="$route.path === '/login' || $route.meta.popup" />
  <div v-else class="shell" :class="{'sidebar-collapsed':collapsed}">
    <aside>
      <router-link class="brand" to="/"><img class="brand-icon" src="https://raw.githubusercontent.com/dh1888/dh18/main/ico/DHPG.png" alt="TG打卡考勤" /><div>TG打卡考勤<small>考勤管理工作台</small></div></router-link>
      <nav>
        <router-link v-for="item in menus" :key="item.path" :to="item.path" :title="item.label"><el-icon class="menu-icon"><component :is="item.icon" /></el-icon><span class="menu-label">{{item.label}}</span></router-link>
        <button class="settings-toggle" :class="{'group-active':inSettings}" @click="toggleSettings" :aria-expanded="settingsOpen" title="设置"><el-icon class="menu-icon"><Setting /></el-icon><span class="menu-label">设置</span><el-icon v-if="!collapsed" class="group-chevron" :class="{'is-open':settingsOpen}"><ArrowRight /></el-icon></button>
        <div v-show="settingsOpen" class="settings-items">
          <router-link v-for="item in settingsMenus" :key="item.path" :to="item.path" :title="item.label"><el-icon class="menu-icon"><component :is="item.icon" /></el-icon><span class="menu-label">{{item.label}}</span></router-link>
        </div>
      </nav>
    </aside>
    <section class="workspace">
      <header>
        <div class="header-left"><button class="sidebar-toggle" @click="toggleSidebar" :aria-label="collapsed?'展开菜单':'收起菜单'"><el-icon :size="21"><Expand v-if="collapsed"/><Fold v-else/></el-icon></button><span>{{title}}</span></div>
        <div class="header-right"><button v-if="user.role !== 'admin'" @click="bindTelegram">绑定 TG</button><el-dropdown trigger="hover" placement="bottom-end" :show-timeout="80" :hide-timeout="150"><div class="account-trigger"><el-avatar :size="30" src="/avatar"><el-icon><User /></el-icon></el-avatar><span>{{user.username}}</span></div><template #dropdown><el-dropdown-menu><el-dropdown-item :icon="Key" @click="router.push('/password')">修改密码</el-dropdown-item><el-dropdown-item :icon="SwitchButton" divided @click="user.logout">退出登录</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div>
      </header>
      <WorkspaceTabs :key="user.userId" />
    </section>
  </div>
</template>
<script setup lang="ts">
import {ElMessage,ElMessageBox} from 'element-plus';
import {Odometer,User,Setting,Tickets,Position,Calendar,Checked,Wallet,Lock,Key,Delete,ArrowRight,Fold,Expand,SwitchButton} from '@element-plus/icons-vue';
import WorkspaceTabs from './components/WorkspaceTabs.vue';
import request from './api/request';
import {computed,ref,watch,onMounted,onUnmounted} from 'vue';
import {useRoute,useRouter} from 'vue-router';
import {useUserStore,canOpenPage} from './store/user';
const user=useUserStore(),route=useRoute(),router=useRouter();
function receivePermissions(event:Event){const next=(event as CustomEvent).detail;if(JSON.stringify(next)!==JSON.stringify(user.permissions))user.permissions=next}
async function refreshIdentity(){if(user.isAuthenticated&&document.visibilityState==='visible'&&!(await user.validateSession()))void router.replace('/login')}
let permissionTimer:ReturnType<typeof setInterval>|undefined;
onMounted(()=>{window.addEventListener('tg-permissions',receivePermissions);window.addEventListener('focus',refreshIdentity);permissionTimer=setInterval(refreshIdentity,30000)});
onUnmounted(()=>{window.removeEventListener('tg-permissions',receivePermissions);window.removeEventListener('focus',refreshIdentity);if(permissionTimer)clearInterval(permissionTimer)});

watch(()=>user.permissions,()=>{if(user.isAuthenticated&&route.path!=='/login'&&!canOpenPage(route.path))router.replace('/password')},{deep:true});
function stored(key:string,fallback:boolean){try{const v=localStorage.getItem(key);return v===null?fallback:v==='true'}catch{return fallback}}
function persist(key:string,value:boolean){try{localStorage.setItem(key,String(value))}catch{}}
const collapsed=ref(stored('tg-sidebar-collapsed',false)),settingsOpen=ref(stored('tg-settings-open',true));
function toggleSidebar(){collapsed.value=!collapsed.value;persist('tg-sidebar-collapsed',collapsed.value)}
function toggleSettings(){settingsOpen.value=!settingsOpen.value;persist('tg-settings-open',settingsOpen.value)}
const allMenus=[{path:'/',label:'首页概览',icon:Odometer},{path:'/telegram-punch',label:'后台打卡',icon:Position},{path:'/telegram-records',label:'打卡记录',icon:Tickets},{path:'/attendance',label:'考勤月历',icon:Calendar},{path:'/penalty-details',label:'罚款详情',icon:Wallet},{path:'/attendance-review',label:'待确认审批',icon:Checked}];
const settingItems=[{path:'/telegram',label:'打卡规则',icon:Setting},{path:'/accounts',label:'子账号管理',icon:User},{path:'/roles',label:'角色管理',icon:Lock},{path:'/password',label:'修改密码',icon:Key},{path:'/memory',label:'清理内存',icon:Delete}];
const menus=computed(()=>allMenus.filter(x=>canOpenPage(x.path))),settingsMenus=computed(()=>settingItems.filter(x=>canOpenPage(x.path)));
const inSettings=computed(()=>settingItems.some(x=>x.path===route.path));
const title=computed(()=>[...allMenus,...settingItems].find(x=>x.path===route.path)?.label||'TG打卡考勤');
async function bindTelegram(){try{await ElMessageBox.prompt('请输入你的数字 Telegram 用户 ID，保存后绑定到当前子账号。','绑定 TG',{inputPlaceholder:'Telegram 用户 ID',inputValidator:v=>/^[1-9]\d{0,15}$/.test((v||'').trim())||'请输入有效的数字 TG ID',confirmButtonText:'保存绑定',closeOnClickModal:false,beforeClose:async(action,instance,done)=>{if(action!=='confirm'){done();return}instance.confirmButtonLoading=true;try{await request.post('/standalone/bind-telegram',{telegramUserId:instance.inputValue.trim()});ElMessage.success('TG 绑定成功');done()}catch(e:any){ElMessage.error(e.message||'绑定失败')}finally{instance.confirmButtonLoading=false}}})}catch{}}

</script>
<style>
* {
  box-sizing: border-box;
}
body {
  margin: 0;
  font-family:
    Inter,
    -apple-system,
    BlinkMacSystemFont,
    "Segoe UI",
    "PingFang SC",
    sans-serif;
  color: #233146;
  background: #f5f7fa;
}
a {
  color: inherit;
  text-decoration: none;
}
button {
  cursor: pointer;
}
.shell {
  --workspace-bar-height: 52px;
  --sidebar-width: 228px;
  display: flex;
  min-height: 100vh;
}
aside {
  width: var(--sidebar-width);
  background: #102b2d;
  color: #b8cbca;
  position: fixed;
  inset: 0 auto 0 0;
  padding: 30px 19px;
  display: flex;
  flex-direction: column;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  color: white;
  font-weight: 650;
  font-size: 17px;
}
.brand small {
  display: block;
  font-size: 11px;
  color: #8da9a7;
  font-weight: 400;
  margin-top: 6px;
}
.brand-icon {
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  border-radius: 10px;
  object-fit: contain;
  background: #fff;
}
.nav-caption {
  font-size: 9px;
  letter-spacing: 1.5px;
  color: #72918f;
  margin: 50px 12px 17px;
}
nav {
  margin-top: 40px;
  display: grid;
  gap: 6px;
}
nav a {
  padding: 13px;
  border-radius: 8px;
  font-size: 15px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.menu-icon { font-size: 20px; flex: 0 0 20px; }
nav a:hover {
  background: #1a3c3d;
}
nav a.router-link-exact-active {
  background: #254b46;
  color: #d6f4cb;
}
.aside-bottom {
  margin-top: auto;
  padding: 25px 8px 0;
  font-size: 11px;
}
.aside-bottom p {
  color: #789996;
  font-size: 10px;
}
.dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #b9edbc;
  margin-right: 5px;
}
.workspace {
  background: radial-gradient(ellipse at 15% 10%, #229ed926, transparent 50%), radial-gradient(ellipse at 85% 35%, #8b5cf621, transparent 50%), radial-gradient(ellipse at 50% 90%, #f59e0b19, transparent 50%), #f5f7fa;
  background-attachment: fixed;
  margin-left: var(--sidebar-width);
  min-width: 0;
  flex: 1;
}
.workspace > header {
  height: var(--workspace-bar-height);
  position: sticky;
  top: 0;
  z-index: 100;
  background: white;
  border-bottom: 1px solid #e6ebee;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 34px;
  font-size: 14px;
}
.header-right {
  display: flex;
  gap: 20px;
  align-items: center;
  font-size: 12px;
  color: #71808e;
}
.header-right button {
  border: 0;
  background: transparent;
  color: #45615c;
}
main {
  padding: 30px;
  max-width: 1650px;
  margin: auto;
}
.workspace .bg-decoration { display: none !important; }
.workspace .attendance-management { background: transparent !important; }
footer {
  font-size: 10px;
  letter-spacing: 0.5px;
  color: #89979c;
  padding: 15px 32px 25px;
}
.el-button--primary {
  --el-button-bg-color: #205c50;
  --el-button-border-color: #205c50;
  --el-button-hover-bg-color: #347567;
  --el-button-hover-border-color: #347567;
}
.page-title {
  margin: 0 0 8px;
  font-size: 26px;
  font-weight: 600;
}
.muted {
  color: #819096;
  font-size: 13px;
  line-height: 1.8;
}
.panel {
  background: white;
  border: 1px solid #e3e9eb;
  border-radius: 13px;
  padding: 24px;
  margin-top: 24px;
}
.panel h3 {
  margin: 0 0 20px;
  font-size: 15px;
}
.el-table {
  font-size: 12px !important;
}
.sidebar-collapsed{--sidebar-width:72px}
.sidebar-collapsed aside{padding:24px 9px}
.sidebar-collapsed .brand div,.sidebar-collapsed .menu-label{display:none}
.sidebar-collapsed nav a,.sidebar-collapsed .settings-toggle{justify-content:center;padding:13px}
.sidebar-collapsed .settings-items{padding-left:0}
aside{overflow-y:auto;scrollbar-width:none;z-index:110}aside::-webkit-scrollbar{display:none}
.settings-toggle{display:flex;align-items:center;gap:12px;width:100%;padding:13px;border:0;background:transparent;color:inherit;font-size:15px;border-radius:8px;text-align:left}
.settings-toggle:hover,.settings-toggle.group-active{background:#1a3c3d;color:#d6f4cb}
.group-chevron{margin-left:auto;transition:transform .2s}.group-chevron.is-open{transform:rotate(90deg)}
.settings-items{display:grid;gap:5px;padding-left:10px}
.account-trigger{display:flex;align-items:center;gap:8px;cursor:pointer;padding:4px 8px;border-radius:8px;color:#45615c;outline:none}.account-trigger:hover{background:#f0f5f3}.account-trigger span{font-size:13px}
.header-left{display:flex;align-items:center;gap:14px}.sidebar-toggle{border:0;background:transparent;color:#45615c;display:flex;padding:6px}
@media(max-width:900px){main{padding:16px}.workspace>header{padding:0 12px}.header-right{gap:8px}.header-right>span{display:none}}
</style>

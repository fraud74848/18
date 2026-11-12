<template>
  <section class="management-page" :class="{'accounts-page':mode==='accounts'}">

    <div v-if="mode==='password'" class="pw-page">
      <section class="pw-card">
        <header class="pw-head">
          <span class="pw-badge" aria-hidden="true"><svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="4.5" y="10.5" width="15" height="10" rx="2.5"/><path d="M8 10.5V8a4 4 0 0 1 8 0v2.5"/><circle cx="12" cy="15.5" r="1.2" fill="currentColor" stroke="none"/></svg></span>
          <div>
            <h2>修改登录密码</h2>
            <p>当前账号：<b>{{user.username}}</b>。保存后会退出所有设备，需要用新密码重新登录。</p>
          </div>
        </header>
        <el-form class="pw-form" label-position="top" @submit.prevent="changePassword">
          <el-form-item label="当前密码">
            <el-input v-model="password.currentPassword" type="password" show-password size="large" placeholder="输入现在使用的密码" autocomplete="current-password" />
          </el-form-item>
          <el-form-item label="新密码">
            <el-input v-model="password.password" type="password" show-password size="large" placeholder="至少 6 位" autocomplete="new-password" />
            <div class="pw-meter" :class="'lv-'+pwStrength.level" aria-live="polite">
              <div class="pw-bars" aria-hidden="true"><i></i><i></i><i></i><i></i></div>
              <span class="pw-meter-text" :class="{bad:pwStrength.bad}">{{pwStrength.label}}</span>
              <span class="pw-bytes" :class="{bad:pwBytes>72}">{{pwBytes}} / 72 字节</span>
            </div>
          </el-form-item>
          <el-form-item label="确认新密码">
            <el-input v-model="password.confirm" type="password" show-password size="large" placeholder="再输入一次新密码" autocomplete="new-password" />
            <p v-if="password.confirm" class="pw-match" :class="pwMatch?'ok':'bad'" aria-live="polite">{{pwMatch?'两次输入一致':'两次输入不一致'}}</p>
          </el-form-item>
          <el-button class="pw-submit" type="primary" native-type="submit" size="large" :loading="saving">保存密码并重新登录</el-button>
        </el-form>
      </section>
      <aside class="pw-side" aria-label="密码说明">
        <section class="pw-side-sec">
          <h3><span class="pw-side-ico" aria-hidden="true"><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 5 6v5.5c0 4.2 2.9 7.6 7 9.5 4.1-1.9 7-5.3 7-9.5V6z"/><path d="m9 12 2.2 2.2L15.2 10"/></svg></span>密码要求</h3>
          <ul class="pw-rules">
            <li :class="{ok:pwLenOk}"><svg viewBox="0 0 20 20" width="18" height="18" aria-hidden="true"><circle cx="10" cy="10" r="8.5"/><path d="m6.2 10.3 2.5 2.5 5-5.4"/></svg><span>长度 6–72 字节<small>一个中文字符约占 3 字节</small></span></li>
            <li :class="{ok:pwMatch}"><svg viewBox="0 0 20 20" width="18" height="18" aria-hidden="true"><circle cx="10" cy="10" r="8.5"/><path d="m6.2 10.3 2.5 2.5 5-5.4"/></svg><span>两次输入一致</span></li>
          </ul>
        </section>
        <section class="pw-side-sec">
          <h3><span class="pw-side-ico" aria-hidden="true"><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7.5V12l3 2"/></svg></span>保存之后</h3>
          <p class="pw-note">该账号所有登录会话会立即退出，用新密码重新登录即可。</p>
          <p class="pw-note">忘记密码时，请联系有「重置密码」权限的管理员，在「子账号管理」中重置。</p>
        </section>
      </aside>
    </div>
    <section v-else-if="mode==='memory'" class="panel resource-panel">
      <div class="panel-toolbar"><h3>容量监控与安全清理</h3><div><el-button @click="loadMemory">刷新</el-button><el-button v-permission="'maintenance:edit'" @click="quotaVisible=true">⚙ 设置</el-button><el-button v-permission="'maintenance:clean'" type="primary" :loading="saving" @click="cleanMemory">手动安全清理</el-button></div></div>
      <el-alert v-for="alert in visibleAlerts(memory.snapshot?.alerts)" :key="alert" :title="alert" type="warning" :closable="false" show-icon class="resource-alert"/>
      <div class="resource-metrics">
        <article><span>服务器磁盘</span><strong>{{memory.snapshot?.diskTotal ? memory.snapshot.diskPercent.toFixed(1)+'%' : '监控不可用'}}</strong><el-progress :percentage="Math.min(100,memory.snapshot?.diskPercent||0)" :status="memory.snapshot?.diskPercent>80?'exception':undefined"/><small>可用 {{bytes(memory.snapshot?.diskAvailable)}} / 总计 {{bytes(memory.snapshot?.diskTotal)}}</small></article>
        <article><span>当前外部数据库</span><strong>{{memory.snapshot?.databasePercent == null ? '待配置额度' : memory.snapshot.databasePercent.toFixed(1)+'%'}}</strong><el-progress :percentage="Math.min(100,memory.snapshot?.databasePercent||0)" :status="memory.snapshot?.databasePercent>80?'exception':undefined"/><small>已用 {{bytes(memory.snapshot?.databaseBytes)}}，按本数据库额度统计</small></article>
        <article><span>服务内存</span><strong>{{bytes(memory.heapBytes)}}</strong><small>运行时保留 {{bytes(memory.systemBytes)}}；清理时尝试归还空闲内存</small></article>
      </div>
      <h3>保留策略</h3>
      <p class="muted">每分钟监测，超过 80% 触发；执行间隔至少 30 分钟。只清理过期会话、绑定码、通知、已处理消息、维护日志及已有完整备份的过期导出临时副本。考勤、账号、有效备份和数据库底层文件不在清理范围。</p>
      <el-form label-position="top" class="resource-policy" :disabled="!user.hasPermission('maintenance:edit') || saving">
        <el-form-item label="自动安全清理"><el-switch v-model="policy.auto"/></el-form-item>
        <el-form-item label="数据库容量额度（GiB；0 表示未知）"><el-input-number v-model="quotaGiB" :min="0" :precision="3"/></el-form-item>
        <el-form-item label="已处理消息 / 通知保留天数"><el-input-number v-model="policy.historyDays" :min="30" :max="3650"/></el-form-item>
        <el-form-item label="维护日志保留天数"><el-input-number v-model="policy.logDays" :min="90" :max="3650"/></el-form-item>
        <el-form-item label="已备份导出临时副本保留小时"><el-input-number v-model="policy.tempHours" :min="24" :max="8760"/></el-form-item>
      </el-form>
      <el-button v-permission="'maintenance:edit'" :loading="saving" @click="savePolicy">保存策略</el-button>
      <p class="muted">数据库额度请填写服务商分配给本数据库的容量，不代表服务商整台磁盘使用率。安全 VACUUM 回收可复用空间并尝试释放空闲尾页；不会自动执行需要锁表和额外空间的 FULL。下表为执行前后观测差值，可能受并发写入影响，不将删除行数当作磁盘释放量。</p>
      <h3>执行日志</h3><el-table :data="memory.history||[]" empty-text="尚无清理记录">
        <el-table-column type="expand"><template #default="{row}"><div class="resource-detail"><p v-for="(step,i) in row.steps" :key="i">{{step}}</p><p v-for="(error,i) in row.errors" :key="'error'+i" class="resource-error">{{error}}</p><p v-for="(alert,i) in visibleAlerts(row.after?.alerts)" :key="'alert'+i" class="resource-error">{{alert}}</p></div></template></el-table-column>
        <el-table-column label="执行时间" min-width="180"><template #default="{row}">{{new Date(row.startedAt).toLocaleString()}}</template></el-table-column>
        <el-table-column label="触发方式" width="100"><template #default="{row}">{{row.trigger==='automatic'?'自动':'手动'}}</template></el-table-column>
        <el-table-column label="状态" width="110"><template #default="{row}"><el-tag :type="row.status==='completed'?'success':'warning'">{{runStatus[row.status]||row.status}}</el-tag></template></el-table-column>
        <el-table-column prop="deletedRows" label="清理行数" width="100"/>
        <el-table-column label="磁盘可用增量" min-width="130"><template #default="{row}">{{bytes(row.observedDiskReleased)}}</template></el-table-column>
        <el-table-column label="数据库体积减少" min-width="140"><template #default="{row}">{{bytes(row.observedDatabaseReleased)}}</template></el-table-column>
      </el-table>
      <el-pagination class="list-pagination" background layout="total, prev, pager, next" :total="memory.total||0" :page-size="10" :current-page="memoryPage" :disabled="memoryLoading||saving" @current-change="changeMemoryPage"/>
    </section>
    <section v-else-if="mode==='roles'" class="panel">
      <div class="panel-toolbar"><h3>角色与权限</h3><el-button v-permission="'roles:edit'" type="primary" @click="editRole()">＋ 创建角色</el-button></div>
      <el-table :data="roles" v-loading="loading" empty-text="暂无角色，请创建一个角色">
        <el-table-column prop="name" label="角色名称" min-width="180"/><el-table-column prop="userCount" label="关联账号" width="100"/>
        <el-table-column label="权限" min-width="220"><template #default="{row}"><el-tag v-if="row.protected">系统管理员</el-tag><span v-else>{{row.permissions.length ? row.permissions.length+' 项授权' : '未授权：无业务页面访问权限'}}</span></template></el-table-column>
        <el-table-column label="操作" width="170"><template #default="{row}"><el-button v-permission="'roles:edit'" link :disabled="row.protected" @click="editRole(row)">编辑</el-button><el-button v-permission="'roles:delete'" link type="danger" :disabled="row.protected || row.name==='查看员' || row.userCount>0" @click="deleteRole(row)">删除</el-button></template></el-table-column>
      </el-table>
    </section>
    <section v-else class="panel">
      <div class="panel-toolbar accounts-toolbar">
        <h3>员工子账号</h3>
        <div class="accounts-actions">
          <el-input v-model="accountSearch" class="accounts-search" clearable placeholder="搜索账号、员工姓名、工号或 TG ID" @keyup.enter="searchAccounts">
            <template #prefix><svg class="search-icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg></template>
          </el-input>
          <el-button v-permission="'accounts:edit'" class="accounts-add" type="primary" @click="openCreate"><span class="plus">＋</span>新增子账号</el-button>
          <div class="global-totp"><span>全局谷歌验证</span><el-switch :model-value="globalTotp" :loading="securitySaving" :disabled="!user.hasPermission('accounts:security')" @change="value=>setLoginSecurity(null,!!value)"/></div>
        </div>
      </div>
      <el-table :data="rows" v-loading="loading"><el-table-column prop="username" label="账号" min-width="130" class-name="big-col"/><el-table-column prop="name" label="员工" min-width="110" class-name="big-col"/><el-table-column label="TG ID" min-width="150" class-name="tg-col"><template #default="{row}"><template v-if="row.telegramUserId"><el-tooltip v-if="user.hasPermission('accounts:edit')" content="点击更改绑定" placement="top"><el-button link @click="binding(row)">{{row.telegramUserId}}</el-button></el-tooltip><span v-else>{{row.telegramUserId}}</span></template><el-button v-else-if="user.hasPermission('accounts:edit')" link @click="binding(row)">绑定 TG</el-button><span v-else class="muted">未绑定</span></template></el-table-column>
        <el-table-column label="在线状态" min-width="175"><template #header><el-tooltip content="在线：最近 2 分钟内后台有活动且登录会话有效；否则显示最后登录时间"><span>在线状态 ⓘ</span></el-tooltip></template><template #default="{row}"><el-tag v-if="row.online" round type="success">在线</el-tag><span v-else-if="row.lastLoginAt">{{new Date(row.lastLoginAt).toLocaleString()}}</span><span v-else class="muted">从未登录</span></template></el-table-column>
        <el-table-column label="谷歌验证" min-width="180"><template #default="{row}"><div class="totp-cell"><el-switch :model-value="row.totpRequired" :disabled="securitySaving||!user.hasPermission('accounts:security')" @change="value=>setLoginSecurity(row,!!value)"/><span>{{globalTotp?'全局强制':row.totpRequired?'个人开启':'普通验证码'}}</span><small v-if="globalTotp||row.totpRequired">{{row.totpBound?'已绑定':'下次登录绑定'}}</small></div></template></el-table-column>
        <el-table-column label="状态" width="110"><template #default="{row}"><el-switch :model-value="row.status===1" inline-prompt active-text="启用" inactive-text="禁用" :loading="pendingAccounts.has(row.id)" :disabled="!user.hasPermission('accounts:edit')" @change="toggle(row)"/></template></el-table-column>
        <el-table-column label="操作" min-width="370" class-name="op-col"><template #default="{row}">
          <el-button v-if="globalTotp||row.totpRequired" v-permission="'accounts:security'" link type="warning" :disabled="securitySaving" @click="setLoginSecurity(row,row.totpRequired,true)">重置谷歌验证码</el-button><el-button v-permission="'roles:assign'" link @click="assign(row)">分配角色</el-button><el-button v-permission="'accounts:reset_password'" link @click="resetPassword(row)">重置密码</el-button><el-button v-permission="'accounts:reset'" link type="warning" :loading="pendingAccounts.has(row.id)" @click="resetAttendance(row)">重置打卡</el-button>
        </template></el-table-column>
      </el-table>
      <el-pagination class="list-pagination" background layout="total, prev, pager, next, jumper" :total="accountTotal" :page-size="10" :current-page="accountPage" :disabled="loading" @current-change="changeAccountPage"/>
    </section>
    <el-dialog v-model="quotaVisible" title="外部数据库配额设置" width="460px"><el-form label-position="top" @submit.prevent="saveQuota"><el-form-item label="数据库容量额度（GiB；0 表示未知）"><el-input-number v-model="quotaGiB" :min="0" :precision="3" style="width:100%"/></el-form-item><p class="muted">请填写服务商分配给本数据库的容量（例如 5 GiB），用于判断数据库使用率是否超过 80%。</p></el-form><template #footer><el-button @click="quotaVisible=false">取消</el-button><el-button type="primary" :loading="saving" v-permission="'maintenance:edit'" @click="saveQuota">保存</el-button></template></el-dialog>
    <el-dialog v-model="roleVisible" :title="roleForm.id?'编辑角色':'创建角色'" width="min(960px, 95vw)">
      <el-form label-position="top"><el-form-item label="角色名称"><el-input v-model="roleForm.name" maxlength="100" :disabled="!!roleForm.id && roleForm.name==='查看员'"/></el-form-item></el-form>
      <el-alert :closable="false" type="info" title="编辑自动包含查看；删除、审核、审批、导出等操作需单独勾选。全员数据权限决定是否能管理其他员工。"/>
      <div class="permission-matrix"><div v-for="group in catalog" :key="group.key" class="permission-row"><strong>{{group.label}}</strong><div><el-checkbox v-for="action in group.actions" :key="action" :model-value="roleForm.permissions.includes(group.key+':'+action)" :disabled="!user.hasPermission(group.key+':'+action) || (action==='view' && roleForm.permissions.includes(group.key+':edit'))" @change="value=>setGrant(group.key,action,!!value)">{{group.key==='punch' && action==='edit' ? '打卡' : actionLabels[action]||action}}</el-checkbox></div></div></div>
      <template #footer><el-button @click="roleVisible=false">取消</el-button><el-button type="primary" :loading="saving" v-permission="'roles:edit'" @click="saveRole">保存角色</el-button></template>
    </el-dialog>
    <el-dialog v-model="assignVisible" :title="'分配角色 · '+target?.username" width="480px"><el-alert title="清空角色后，该账号将无法访问业务菜单和接口。" type="warning" :closable="false"/><el-select v-model="assigned" multiple placeholder="选择角色" style="width:100%;margin-top:20px"><el-option v-for="r in roles.filter(x=>!x.protected)" :key="r.id" :value="r.id" :label="r.name"/></el-select><template #footer><el-button @click="assignVisible=false">取消</el-button><el-button type="primary" :loading="saving" v-permission="'roles:assign'" @click="saveAssignment">保存授权</el-button></template></el-dialog>
    <el-dialog v-model="visible" title="新增子账号" width="460px"><el-form label-position="top"><el-form-item label="关联员工"><el-select v-model="form.employeeId" filterable style="width:100%"><el-option v-for="e in employees" :key="e.id" :value="e.id" :label="e.name"/></el-select></el-form-item><el-form-item label="账号（3–64位字母、数字或_.-）"><el-input v-model="form.username"/></el-form-item><el-form-item label="初始密码（至少6位）"><el-input v-model="form.password" type="password" show-password/></el-form-item></el-form><template #footer><el-button @click="visible=false">取消</el-button><el-button type="primary" :loading="saving" v-permission="'accounts:edit'" @click="create">创建</el-button></template></el-dialog>
    <el-dialog v-model="bindVisible" :title="'绑定 Telegram · '+bindTarget?.username" width="480px"><el-form label-position="top" @submit.prevent="saveBinding"><el-form-item label="Telegram 用户 ID"><el-input v-model="telegramId" inputmode="numeric" placeholder="请输入数字 TG ID" maxlength="16"/></el-form-item><p class="muted">保存后替换该员工原有的 TG 绑定；不能使用其他员工已绑定的 ID。</p><el-button type="primary" native-type="submit" :loading="saving">保存绑定</el-button></el-form></el-dialog>
  </section>
</template>
<script setup lang="ts">
import {ref,computed,watch,onMounted,onUnmounted} from 'vue';
import {useRoute} from 'vue-router';
import {ElMessage,ElMessageBox} from 'element-plus';
import request from '../../api/request';
import {useUserStore} from '../../store/user';
const user=useUserStore(),route=useRoute();
const mode=computed(()=>route.path.slice(1));
const rows=ref<any[]>([]),roles=ref<any[]>([]),catalog=ref<any[]>([]),employees=ref<any[]>([]),loading=ref(false),saving=ref(false),visible=ref(false),bindVisible=ref(false),form=ref({username:'',password:'',employeeId:''});
const roleVisible=ref(false),assignVisible=ref(false),target=ref<any>(null),assigned=ref<string[]>([]),roleForm=ref({id:'',name:'',permissions:[] as string[]}),password=ref({currentPassword:'',password:'',confirm:''}),memory=ref<any>({});
const pwBytes=computed(()=>new TextEncoder().encode(password.value.password).length);
const pwLenOk=computed(()=>pwBytes.value>=6&&pwBytes.value<=72);
const pwMatch=computed(()=>!!password.value.confirm&&password.value.confirm===password.value.password);
const pwStrength=computed(()=>{const v=password.value.password,n=pwBytes.value;if(!v)return{level:0,bad:false,label:'输入后显示强度'};if(n<6)return{level:0,bad:true,label:'太短，至少 6 字节'};if(n>72)return{level:0,bad:true,label:'超过 72 字节'};const len=[...v].length;let sc=0;if(len>=8)sc++;if(len>=12)sc++;if((/[a-z]/.test(v)&&/[A-Z]/.test(v))||(/\d/.test(v)&&/[A-Za-z]/.test(v)))sc++;if(/[^A-Za-z0-9]/.test(v))sc++;const level=Math.max(1,sc);return{level,bad:false,label:['','强度较弱','强度一般','强度较强','强度很强'][level]}});
const actionLabels:Record<string,string>={view:'查看',edit:'编辑',delete:'删除',review:'审核',approve:'审批',export:'导出',assign:'分配角色',reset_password:'重置密码',reset:'重置打卡',notify:'发送通知',clean:'执行清理',all:'管理全员数据',security:'登录验证'};
function validPassword(v:string){const n=new TextEncoder().encode(v).length;return n>=6&&n<=72}
function report(e:any){if(e!=='cancel'&&e!=='close')ElMessage.error(e.message||'操作失败')}
const globalTotp=ref(false),securitySaving=ref(false);
async function loadLoginPolicy(){const r:any=await request.get('/standalone/login-policy');globalTotp.value=r.data.global}
async function setLoginSecurity(row:any,enabled:boolean,reset=false){securitySaving.value=true;try{if(reset&&row){await ElMessageBox.confirm('重置后，'+row.username+' 下次登录需要重新绑定谷歌验证器，其登录会话将退出。','重置谷歌验证码',{type:'warning',confirmButtonText:'重置',cancelButtonText:'取消',closeOnClickModal:false});await request.put('/standalone/accounts/'+row.id+'/totp',{enabled:row.totpRequired,reset:true});await Promise.all([load(),loadLoginPolicy()]);ElMessage.success('谷歌验证码已重置');return}const text=reset?'重置后，该用户下次需要重新绑定验证器。':row?(enabled?'开启该用户谷歌验证。':'关闭个人开关；全局开启时仍需谷歌验证。'):(enabled?'管理员及全部子账号都将使用谷歌验证。':'关闭全局强制；个人已开启的用户仍需谷歌验证。');const {value}=await ElMessageBox.prompt(text+' 其他受影响的登录会话将退出。请输入你当前的登录密码确认。',reset?'重置谷歌绑定':'登录验证设置',{inputType:'password',inputValidator:v=>!!v||'请输入当前密码',closeOnClickModal:false});await request.put(row?'/standalone/accounts/'+row.id+'/totp':'/standalone/login-policy',{enabled,password:value,reset});await Promise.all([load(),loadLoginPolicy()]);ElMessage.success('登录验证设置已更新')}catch(e){report(e)}finally{securitySaving.value=false}}
const accountSearch=ref(''),appliedSearch=ref(''),accountPage=ref(1),accountTotal=ref(0);
let searchTimer=0;
async function searchAccounts(){window.clearTimeout(searchTimer);appliedSearch.value=(accountSearch.value||'').trim();accountPage.value=1;try{await load()}catch(e){report(e)}}
watch(accountSearch,()=>{if(mode.value!=='accounts')return;window.clearTimeout(searchTimer);searchTimer=window.setTimeout(()=>{void searchAccounts()},300)});
async function changeAccountPage(page:number){accountPage.value=page;try{await load()}catch(e){report(e)}}
let loadSeq=0;
async function load(silent=false){const seq=++loadSeq;if(!silent)loading.value=true;try{const r:any=await request.get('/standalone/accounts',{params:{search:appliedSearch.value,page:accountPage.value,pageSize:10}});if(seq!==loadSeq)return;rows.value=r.data.items;accountPage.value=r.data.page;accountTotal.value=r.data.total}finally{if(seq===loadSeq)loading.value=false}}
async function loadRoles(){const r:any=await request.get('/standalone/roles');roles.value=r.data.items;catalog.value=r.data.catalog}
function editRole(row?:any){roleForm.value={id:row?.id||'',name:row?.name||'',permissions:[...(row?.permissions||[])]};roleVisible.value=true}
function setGrant(module:string,action:string,checked:boolean){const set=new Set(roleForm.value.permissions),key=module+':'+action;if(checked){set.add(key);if(action==='edit')set.add(module+':view')}else set.delete(key);roleForm.value.permissions=[...set]}
async function saveRole(){saving.value=true;try{const f=roleForm.value;if(f.id)await request.put('/standalone/roles/'+f.id,f);else await request.post('/standalone/roles',f);roleVisible.value=false;await user.validateSession();await loadRoles();ElMessage.success('角色已保存')}catch(e){report(e)}finally{saving.value=false}}
async function deleteRole(row:any){try{await ElMessageBox.confirm('确认删除角色 '+row.name+'？','删除角色');await request.delete('/standalone/roles/'+row.id);await loadRoles()}catch(e){report(e)}}
async function assign(row:any){try{await loadRoles();target.value=row;assigned.value=[...(row.roleIds||[])];assignVisible.value=true}catch(e){report(e)}}
async function saveAssignment(){saving.value=true;try{await request.put('/standalone/accounts/'+target.value.id+'/roles',{roleIds:assigned.value});assignVisible.value=false;await user.validateSession();await load();ElMessage.success('授权已更新')}catch(e){report(e)}finally{saving.value=false}}
async function openCreate(){try{const r:any=await request.get('/admin/employees',{params:{limit:1000,employmentScope:'all'}});employees.value=(r.data.items||[]).filter((e:any)=>!e.userId&&e.status===1);form.value={username:'',password:'',employeeId:''};visible.value=true}catch(e){report(e)}}
async function create(){if(!validPassword(form.value.password)){ElMessage.warning('密码需要6–72字节');return}saving.value=true;try{await request.post('/standalone/accounts',form.value);visible.value=false;form.value.password='';await load();ElMessage.success('子账号已创建')}catch(e){report(e)}finally{saving.value=false}}
const bindTarget=ref<any>(null),telegramId=ref(''),pendingAccounts=ref(new Set<string>());
function binding(row:any){bindTarget.value=row;telegramId.value=row.telegramUserId||'';bindVisible.value=true}
async function saveBinding(){if(!/^[1-9]\d{0,15}$/.test(telegramId.value.trim())){ElMessage.warning('请输入有效的数字 TG ID');return}saving.value=true;try{await request.post('/standalone/accounts/'+bindTarget.value.id+'/telegram',{telegramUserId:telegramId.value.trim()});bindVisible.value=false;await load();ElMessage.success('TG 绑定已保存')}catch(e){report(e)}finally{saving.value=false}}
async function resetAttendance(row:any){if(pendingAccounts.value.has(row.id))return;pendingAccounts.value.add(row.id);try{await ElMessageBox.confirm('将重置 '+row.name+' 当前业务日的上班、下班及活动打卡次数。进行中的活动和上班记录会结束，原记录归档保留，之后可重新打卡。确认继续？','重置打卡',{confirmButtonText:'确认重置',type:'warning'});await request.post('/standalone/accounts/'+row.id+'/reset-daily');ElMessage.success('该员工当日打卡已重置');await load()}catch(e){report(e)}finally{pendingAccounts.value.delete(row.id)}}
async function toggle(row:any){if(pendingAccounts.value.has(row.id))return;pendingAccounts.value.add(row.id);try{await ElMessageBox.confirm('确认'+(row.status===1?'禁用':'启用')+' '+row.username+'？现有登录会话将失效。','账号状态');await request.put('/standalone/accounts/'+row.id,{status:row.status===1?0:1});await load()}catch(e){report(e)}finally{pendingAccounts.value.delete(row.id)}}
async function resetPassword(row:any){try{const {value}=await ElMessageBox.prompt('为 '+row.username+' 设置新密码（6–72字节）','重置密码',{inputType:'password',inputValidator:v=>validPassword(v||'')||'密码长度不符合要求'});await request.post('/standalone/accounts/'+row.id+'/password',{password:value});ElMessage.success('密码已重置，旧会话已退出')}catch(e){report(e)}}
async function changePassword(){if(!validPassword(password.value.password)||password.value.password!==password.value.confirm){ElMessage.warning('请填写6–72字节的新密码，并确保两次输入一致');return}saving.value=true;try{await request.post('/auth/password',password.value);ElMessage.success('密码已修改，请重新登录');location.assign('/login')}catch(e){report(e)}finally{saving.value=false}}
const runStatus:Record<string,string>={completed:'完成',partial:'有异常',running:'执行中'};
const quotaVisible=ref(false);
const policy=ref({auto:true,historyDays:30,logDays:180,tempHours:72}),quotaGiB=ref(0);
function bytes(n:number=0){if(!Number.isFinite(n)||n<=0)return '0 B';const units=['B','KiB','MiB','GiB','TiB'];const i=Math.min(4,Math.floor(Math.log(n)/Math.log(1024)));return (n/1024**i).toFixed(1)+' '+units[i]}
const memoryPage=ref(1),memoryLoading=ref(false);
function visibleAlerts(alerts:string[]=[]){return alerts.filter(a=>!a.startsWith('服务器磁盘使用率超过80%')&&!a.startsWith('未设置外部数据库配额'))}
async function changeMemoryPage(page:number){memoryPage.value=page;await loadMemory(false)}
async function loadMemory(refreshPolicy=true){memoryLoading.value=true;try{const r:any=await request.get('/standalone/memory',{params:{page:memoryPage.value,pageSize:10}});memory.value=r.data;memoryPage.value=r.data.page;if(refreshPolicy){policy.value={...r.data.policy};quotaGiB.value=r.data.policy.databaseQuotaBytes/1024**3}}catch(e){report(e)}finally{memoryLoading.value=false}}
async function savePolicy(){saving.value=true;try{const r:any=await request.put('/standalone/memory/policy',{...policy.value,databaseQuotaBytes:Math.round(quotaGiB.value*1024**3)});memory.value=r.data;memoryPage.value=r.data.page;ElMessage.success('清理策略已保存');return true}catch(e){report(e);return false}finally{saving.value=false}}
async function saveQuota(){if(await savePolicy())quotaVisible.value=false}
async function cleanMemory(){saving.value=true;try{const r:any=await request.post('/standalone/memory/clean');memory.value=r.data;memoryPage.value=r.data.page;if(r.data.history?.[0]?.status==='partial')ElMessage.warning('清理结束，部分项目异常，请查看执行日志');else ElMessage.success('清理结束，请查看实际空间变化')}catch(e){report(e);await loadMemory(false)}finally{saving.value=false}}
const resourceTimer=window.setInterval(()=>{if(mode.value==='accounts'&&!saving.value&&!document.hidden&&!loading.value)void load(true).catch(()=>{})},30000);
onUnmounted(()=>{window.clearInterval(resourceTimer);window.clearTimeout(searchTimer)});
onMounted(async()=>{try{if(mode.value==='roles')await loadRoles();else if(mode.value==='memory')await loadMemory();else if(mode.value==='accounts')await Promise.all([load(),loadLoginPolicy()])}catch(e){report(e)}});
</script>
<style scoped>
.global-totp{display:flex;align-items:center;gap:10px;font-size:13px;color:#385b77}.totp-cell{display:flex;align-items:center;flex-wrap:wrap;gap:6px}.totp-cell small{width:100%;color:#71867d}

.list-pagination{margin-top:18px;overflow-x:auto}
.accounts-toolbar{padding-bottom:18px;border-bottom:1px solid #e8efec}
.accounts-actions{display:flex;align-items:center;gap:14px;flex-wrap:wrap;margin-left:auto}
.accounts-search{width:340px;max-width:100%}
.accounts-search :deep(.el-input__wrapper){border-radius:999px;padding:3px 16px;background:#fff;box-shadow:0 0 0 1px #dce7ef inset,0 2px 8px #1b3a3112;transition:box-shadow .2s}
.accounts-search :deep(.el-input__wrapper:hover){box-shadow:0 0 0 1px #b9cfd8 inset,0 2px 8px #1b3a3118}
.accounts-search :deep(.el-input__wrapper.is-focus){box-shadow:0 0 0 1.5px #279c8a inset,0 4px 12px #279c8a26}
.accounts-search .search-icon{color:#7a93a3;margin-right:2px}
.accounts-page .accounts-add.el-button{height:34px;padding:0 20px;font-weight:600;box-shadow:0 4px 12px #279c8a40;transition:transform .15s,box-shadow .15s}
.accounts-page .accounts-add.el-button:hover{transform:translateY(-1px);box-shadow:0 6px 16px #279c8a55}
.accounts-add .plus{margin-right:4px;font-weight:700}
.accounts-toolbar .global-totp{box-sizing:border-box;height:34px;padding:0 8px 0 20px;border:0;border-radius:999px;color:#fff;font-size:14px;font-weight:600;background:linear-gradient(120deg,#279c8a,#6a79d7);box-shadow:0 4px 12px #279c8a40;--el-switch-off-color:#ffffff59}.accounts-toolbar .global-totp :deep(.el-switch.is-checked .el-switch__core){background:#1d5e54;border-color:transparent}
@media(max-width:700px){.accounts-actions{width:100%;margin-left:0}.accounts-search{width:100%}}
.accounts-page :deep(.el-button){border-radius:999px;background:linear-gradient(120deg,#edf7f4,#e9edff);border:1px solid #dce7ef;padding:8px 14px;color:#385b77}
.accounts-page :deep(.el-button--primary){background:linear-gradient(120deg,#279c8a,#6a79d7);color:white;border:0}
.accounts-page :deep(.el-button.is-link){margin:4px 6px 4px 0}
.accounts-page :deep(.el-switch.is-checked .el-switch__core){background:linear-gradient(120deg,#279c8a,#6a79d7);border-color:transparent}
.accounts-page :deep(.el-tag){border-radius:999px;background:linear-gradient(120deg,#edf7f4,#edf0ff)}
.accounts-page .panel{background:linear-gradient(135deg,#fff,#f7fbfa 60%,#f3f4ff);border-radius:24px}
.accounts-page :deep(.el-table){font-size:15px}.accounts-page :deep(.el-table th.el-table__cell){font-size:15px}.accounts-page :deep(.el-table .tg-col .el-button){font-size:15px}.accounts-page :deep(.el-table .big-col .cell){font-size:17px;font-weight:500}.accounts-page :deep(.el-table .op-col .el-button){font-size:14px}.accounts-page :deep(.el-table .el-tag){font-size:14px;height:28px}.accounts-page :deep(.el-table td.el-table__cell){padding:14px 0}.accounts-page .totp-cell small{font-size:13px}

.management-page{width:100%;margin:0}
.management-heading{display:flex;align-items:center;justify-content:space-between;gap:24px;margin:0 0 20px;padding:24px 28px;border-radius:14px;border:1px solid #dbe8e1;background:linear-gradient(120deg,#e8f3ed 0%,#f3f9f5 55%,#fff 100%);position:relative;overflow:hidden}
.management-heading::after{content:"";position:absolute;right:-40px;top:-60px;width:220px;height:220px;border-radius:50%;background:radial-gradient(circle,#b9edbc66,transparent 70%);pointer-events:none}
.management-heading .page-title{margin:0 0 6px;color:#1d3d36}
.management-heading .muted{margin:0}
.eyebrow{display:block;color:#78948e;font-size:11px;letter-spacing:1px;margin-bottom:10px}
.management-page .panel{margin-top:0;width:100%;border-radius:14px;padding:24px 28px;box-shadow:0 2px 12px #1b3a3114;border-color:#e1eae6}
.management-page .panel+.panel{margin-top:20px}
.management-page .panel h3{font-size:16px;color:#1d3d36;padding-left:12px;border-left:3px solid #205c50;line-height:1.1}
.panel-toolbar{display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;gap:12px;flex-wrap:wrap}
.panel-toolbar h3{margin:0!important}
.management-page :deep(.el-table){width:100%;border-radius:10px;overflow:hidden;--el-table-header-bg-color:#f2f7f4;--el-table-row-hover-bg-color:#f5faf7}
.management-page :deep(.el-table th.el-table__cell){color:#345c51;font-weight:600;height:46px}
.management-page :deep(.el-table td.el-table__cell){padding:12px 0}
.pw-page{--pw-ink:#17352f;--pw-text:#2b4a43;--pw-muted:#6a827c;--pw-line:#dfe8e4;--pw-accent:#205c50;--pw-focus:#279c8a;display:grid;grid-template-columns:minmax(0,560px) minmax(260px,360px);gap:28px;align-items:start;justify-content:start}
.pw-card{background:#fff;border:1px solid var(--pw-line);border-radius:18px;padding:32px 36px 36px;box-shadow:0 1px 2px #102b2d0a,0 18px 40px -18px #102b2d33}
.pw-head{display:flex;gap:16px;align-items:flex-start;padding-bottom:24px;margin-bottom:26px;border-bottom:1px solid #edf2ef}
.pw-badge{display:grid;place-items:center;flex:0 0 46px;height:46px;border-radius:14px;background:#e6f1ed;color:var(--pw-accent)}
.pw-head h2{margin:2px 0 6px;font-size:20px;line-height:1.3;font-weight:650;color:var(--pw-ink)}
.pw-head p{margin:0;font-size:13px;line-height:1.7;color:var(--pw-muted)}
.pw-head p b{color:var(--pw-text);font-weight:600}
.pw-form :deep(.el-form-item){margin-bottom:24px}
.pw-form :deep(.el-form-item__label){padding-bottom:8px;line-height:1.2;font-size:13px;font-weight:600;color:var(--pw-text)}
.pw-form :deep(.el-input__wrapper){height:46px;padding:0 14px;border-radius:10px;background:#f7faf9;box-shadow:0 0 0 1px #d6e2dd inset;transition:box-shadow .18s,background-color .18s}
.pw-form :deep(.el-input__wrapper:hover){box-shadow:0 0 0 1px #b3c9c1 inset}
.pw-form :deep(.el-input__wrapper.is-focus){background:#fff;box-shadow:0 0 0 1.5px var(--pw-focus) inset,0 0 0 4px #279c8a22}
.pw-form :deep(.el-input__inner){font-size:15px;color:var(--pw-ink)}
.pw-form :deep(.el-input__inner::placeholder){color:#9db0aa}
.pw-meter{flex:0 0 100%;display:grid;grid-template-columns:1fr auto;grid-template-areas:"bars bars" "text bytes";gap:8px 12px;margin-top:12px;font-size:12px;line-height:1.4}
.pw-bars{grid-area:bars;display:grid;grid-template-columns:repeat(4,1fr);gap:6px}
.pw-bars i{height:4px;border-radius:2px;background:#e3eae7;transition:background-color .2s}
.lv-1 .pw-bars i:nth-child(-n+1){background:#d4685c}
.lv-2 .pw-bars i:nth-child(-n+2){background:#e0a43a}
.lv-3 .pw-bars i:nth-child(-n+3){background:#5aa788}
.lv-4 .pw-bars i:nth-child(-n+4){background:#1f7a66}
.pw-meter-text{grid-area:text;color:var(--pw-muted)}
.pw-bytes{grid-area:bytes;color:#8aa098;font-variant-numeric:tabular-nums}
.pw-meter-text.bad,.pw-bytes.bad{color:#c0483c}
.pw-match{flex:0 0 100%;margin:10px 0 0;font-size:12px;line-height:1.4}
.pw-match.ok{color:#1f7a66}
.pw-match.bad{color:#c0483c}
.pw-form .pw-submit.el-button{display:block;width:100%;height:48px;margin-top:6px;border-radius:12px;font-size:15px;font-weight:600;letter-spacing:.5px;box-shadow:0 8px 18px -8px #205c5099;transition:transform .15s,box-shadow .15s,background-color .15s}
.pw-form .pw-submit.el-button:hover{transform:translateY(-1px);box-shadow:0 12px 22px -10px #205c50b3}
.pw-form .pw-submit.el-button:active{transform:none}
.pw-form .pw-submit.el-button:focus-visible{outline:3px solid #279c8a55;outline-offset:2px}
.pw-side{background:#fff;border:1px solid var(--pw-line);border-radius:18px;padding:8px 24px;box-shadow:0 1px 2px #102b2d0a,0 18px 40px -18px #102b2d33;min-width:0;position:sticky;top:16px}
.pw-side-sec{padding:20px 0}
.pw-side-sec+.pw-side-sec{border-top:1px solid #edf2ef}
.pw-side h3{display:flex;align-items:center;gap:10px;margin:0 0 14px;font-size:14px;font-weight:650;color:var(--pw-ink)}
.pw-side-ico{display:grid;place-items:center;flex:0 0 28px;height:28px;border-radius:9px;background:#e6f1ed;color:var(--pw-accent)}
.pw-rules{list-style:none;margin:0;padding:0;display:grid;gap:12px}
.pw-rules li{display:flex;gap:10px;align-items:flex-start;font-size:13px;line-height:1.5;color:var(--pw-muted);transition:color .2s}
.pw-rules li svg{flex:0 0 18px;margin-top:1px;fill:none;stroke:#c3d1cc;stroke-width:1.5;stroke-linecap:round;stroke-linejoin:round;transition:fill .2s,stroke .2s}
.pw-rules li small{display:block;margin-top:2px;font-size:12px;color:#93a69f}
.pw-rules li.ok{color:var(--pw-text)}
.pw-rules li.ok svg{fill:#1f7a66;stroke:#fff}
.pw-rules li.ok svg circle{stroke:#1f7a66}
.pw-note{margin:0 0 10px;font-size:13px;line-height:1.8;color:var(--pw-muted);overflow-wrap:anywhere}.pw-note:last-child{margin-bottom:0}
@media(prefers-reduced-motion:reduce){.pw-page *{transition:none!important}}
.resource-alert{margin-bottom:12px}
.resource-metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:18px;margin:4px 0 26px}
.resource-metrics article{border:1px solid #dbe8e1;border-radius:14px;padding:22px 24px;background:linear-gradient(160deg,#fff,#f3f9f5);transition:box-shadow .2s,transform .2s}
.resource-metrics article:hover{box-shadow:0 6px 18px #1b3a3120;transform:translateY(-2px)}
.resource-metrics article>span{font-size:13px;color:#72827e}
.resource-metrics strong{display:block;font-size:30px;color:#205c50;margin:14px 0}
.resource-metrics small{display:block;color:#71808e;line-height:1.8;margin-top:12px}
.resource-panel .muted{max-width:none}
.resource-panel>h3{margin-top:26px}
.resource-policy{display:grid;grid-template-columns:repeat(auto-fit,minmax(250px,1fr));gap:4px 24px;padding:20px 22px;margin:14px 0 18px;border:1px solid #e5ece9;border-radius:12px;background:#f7faf8}
.resource-policy :deep(.el-form-item){margin-bottom:12px}
.resource-policy :deep(.el-input-number){width:100%}
.resource-detail{padding:12px 30px}
.resource-error{color:#c45656}
.permission-matrix{margin-top:20px;border:1px solid #e5ece9;border-radius:12px;overflow:hidden;max-height:52vh;overflow-y:auto}
.permission-row{display:grid;grid-template-columns:140px 1fr;gap:20px;padding:15px 20px;border-bottom:1px solid #edf1ef;align-items:center}
.permission-row:last-child{border:0}
.permission-row:nth-child(odd){background:#f7faf8}
.permission-row strong{font-size:14px;color:#345c51}
.memory-value{font-size:42px;font-weight:600;color:#205c50;margin:24px 0}
.memory-value small{font-size:14px;font-weight:400;color:#71808e}
@media(max-width:1000px){.pw-page{grid-template-columns:minmax(0,560px)}.pw-side{position:static}}
@media(max-width:700px){.permission-row{grid-template-columns:1fr;gap:6px}.management-heading{display:block;padding:18px}.pw-card{padding:22px 20px 26px;border-radius:14px}.pw-head{padding-bottom:18px;margin-bottom:20px}.pw-page{gap:20px}.pw-side{padding:4px 18px;border-radius:14px}.pw-side-sec{padding:16px 0}.management-page .panel{padding:18px}}
</style>

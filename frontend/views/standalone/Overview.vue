<template>
  <div>
    <section class="panel attendance-summary" v-loading="busy">
      <h3>今日出勤统计 <span class="muted">{{data.attendance?.date || ''}}</span></h3>
      <div class="metrics">
        <article><span>出勤人数</span><strong>{{data.attendance?.present ?? '—'}}<em> 人</em></strong></article>
        <article v-for="g in peopleGroups" :key="g.key"><span>{{g.label}}人数</span><strong :class="{attention:g.key==='late'}">{{data.attendance?.[g.key] ?? '—'}}<em> 人</em></strong><div class="people-list"><el-tag v-for="person in data.attendance?.people?.[g.key]||[]" :key="person.id" round :type="g.type">{{person.name}}{{person.employeeCode?' · '+person.employeeCode:''}}</el-tag><span v-if="!data.attendance?.people?.[g.key]?.length" class="muted">暂无人员</span></div></article>
      </div>
      <p class="muted">{{data.attendance?.selfOnly?'仅统计本人。':''}}按当日考勤记录去重统计；迟到包含在出勤人数中，休假包含请假、全休和半休。未打卡且未登记旷工的不计入旷工。</p>
    </section>
    <section class="panel">
      <h3>
        消息处理流水 <span class="muted">/ 持久化接收与重复投递去重</span>
      </h3>
      <el-alert
        v-if="error"
        :title="error"
        type="error"
        :closable="false"
      /><el-table
        :data="items"
        empty-text="暂无消息。完成机器人配置后，群内发送 /help 进行验证。"
        ><el-table-column
          prop="updateId"
          label="消息编号"
          width="150"
        /><el-table-column label="接收时间" min-width="170"
          ><template #default="{ row }">{{
            new Date(row.receivedAt).toLocaleString()
          }}</template></el-table-column
        ><el-table-column label="状态" width="110"
          ><template #default="{ row }"
            ><el-tag
              :type="
                row.status === 'uncertain'
                  ? 'warning'
                  : row.status === 'done'
                    ? 'success'
                    : 'info'
              "
              >{{ labels[row.status] || row.status }}</el-tag
            ></template
          ></el-table-column
        ><el-table-column
          prop="note"
          label="说明"
          min-width="220"
        /><el-table-column label="操作" width="100"
          ><template #default="{ row }"
            ><el-button
              v-if="row.status === 'uncertain'"
              link
              type="primary"
              v-permission="'overview:approve'" @click="resolve(row)"
              >确认核对</el-button
            ></template
          ></el-table-column
        ></el-table
      >
      <el-pagination class="list-pagination" background layout="total, prev, pager, next" :total="messageTotal" :page-size="10" :current-page="messagePage" :disabled="busy" @current-change="changeMessages"/>
      <p class="muted" style="font-size: 11px">
        「已处理」表示处理函数已返回；实际打卡是否成功请以业务记录为准。中断的消息不会自动重放，以避免重复罚款。
      </p>
    </section>
    <section class="panel">
      <h3>日重置报表备份 <span class="muted">/ 数据库保存最近 7 天</span></h3>
      <el-table :data="exports" empty-text="日重置生成报表后会显示在这里。"
        ><el-table-column prop="filename" label="文件" /><el-table-column
          label="生成时间"
          ><template #default="{ row }">{{
            new Date(row.createdAt).toLocaleString()
          }}</template></el-table-column
        ><el-table-column label="下载" width="100"
          ><template #default="{ row }"
            ><a
              :href="
                '/api/v1/standalone/exports/' + encodeURIComponent(row.filename)
              "
              download
              >下载 Excel</a
            ></template
          ></el-table-column
        ></el-table
      >
      <el-pagination class="list-pagination" background layout="total, prev, pager, next" :total="exportTotal" :page-size="10" :current-page="exportPage" :disabled="exportsBusy" @current-change="changeExports"/>
    </section>
  </div>
</template>
<script setup lang="ts">
import { ref, onMounted } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import request from "../../api/request";
const peopleGroups=[{key:'late',label:'迟到',type:'warning'},{key:'leave',label:'休假',type:'success'},{key:'absent',label:'旷工',type:'info'}] as const;
const exports = ref<any[]>([]);
const messagePage=ref(1),messageTotal=ref(0),exportPage=ref(1),exportTotal=ref(0),exportsBusy=ref(false);
async function changeMessages(page:number){messagePage.value=page;await loadMessages()}
async function changeExports(page:number){exportPage.value=page;await loadExports()}
async function loadExports(){exportsBusy.value=true;try{const r:any=await request.get('/standalone/exports',{params:{page:exportPage.value,pageSize:10}});exports.value=r.data.items;exportPage.value=r.data.page;exportTotal.value=r.data.total}catch(e:any){ElMessage.error(e.message)}finally{exportsBusy.value=false}}
async function load(){await Promise.all([loadMessages(),loadExports()])}

const data = ref<any>({}),
  items = ref<any[]>([]),
  busy = ref(false),
  error = ref("");
const labels: Record<string, string> = {
  pending: "待处理",
  processing: "处理中",
  done: "已处理",
  uncertain: "待核对",
  resolved: "已核对",
};
async function loadMessages() {
  busy.value = true;
  error.value = "";
  try {
    const r: any = await request.get("/standalone/status",{params:{page:messagePage.value,pageSize:10}});
    data.value = r.data;
    items.value = r.data.items;
    messagePage.value=r.data.page;
    messageTotal.value=r.data.total;
  } catch (e: any) {
    error.value = e.message;
  } finally {
    busy.value = false;
  }
}
async function resolve(row: any) {
  try {
    const r = await ElMessageBox.prompt(
      "请先核对该时段考勤及罚款，必要时在后台修正，再填写结果。不会自动重放消息。",
      "确认核对",
      { inputValidator: (v) => (!!v && v.length >= 4) || "至少填写4个字" },
    );
    await request.post(`/standalone/inbox/${row.updateId}/resolve`, {
      note: r.value,
    });
    await load();
  } catch (e: any) {
    if (e?.message) ElMessage.error(e.message);
  }
}
onMounted(load);
</script>
<style scoped>
.people-list{display:flex;flex-wrap:wrap;gap:8px;max-height:160px;overflow:auto;padding-top:12px;border-top:1px dashed #e3e9eb}

.list-pagination{margin-top:18px;overflow-x:auto}
.attendance-summary{margin-top:0;background:linear-gradient(120deg,#f3faf7,#f5f5ff);border-radius:24px}
.attendance-summary h3{margin-top:0}
.attendance-summary h3 span{font-size:13px;margin-left:12px}
.attendance-summary>p{font-size:12px;margin-bottom:0}

.intro {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.eyebrow,
.section-tag {
  font-size: 10px;
  letter-spacing: 1.5px;
  color: #7b9188;
  margin: 4px 0 15px;
}
.metrics {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
  margin-top: 25px;
}
.metrics article {
  background: white;
  border: 1px solid #e3e9eb;
  border-radius: 12px;
  padding: 23px;
}
.metrics span {
  font-size: 12px;
  color: #72827e;
}
.metrics strong {
  display: block;
  font-size: 37px;
  color: #244c41;
  font-weight: 550;
  margin: 17px 0;
}
.metrics em {
  font-size: 17px;
  font-style: normal;
  color: #b2bfba;
}
.metrics small {
  font-size: 10px;
  color: #92a09a;
}
.metrics .attention {
  color: #ba7843;
}
.setup-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 20px;
}
.setup-grid h3 {
  font-size: 17px;
}
.limits {
  background: #edf2e9;
  border-color: #dde6d8;
}
.limits p {
  font-size: 12px;
  line-height: 1.95;
  color: #5a7063;
  margin: 14px 0;
}
@media (max-width: 1150px) {
  .metrics {
    grid-template-columns: repeat(2, 1fr);
  }
  .setup-grid {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 600px) {
  .metrics {
    grid-template-columns: 1fr;
  }
  .intro {
    align-items: start;
  }
  .page-title {
    font-size: 22px;
  }
}
</style>

<template>
  <div class="login">
    <section class="login-story">
      <span class="eyebrow">TG打卡考勤</span>
      <h1>让每一次打卡，<br />都有清晰的记录。</h1>
      <p>Telegram 打卡 · 多班次 · 活动管理 · 审批与报表</p>
      <div class="orbit">
        <span>✓</span><small>CONNECTED TO YOUR TEAM</small>
      </div>
      <div class="story-footer">独立部署 / 数据自持 / 原有打卡体验</div>
    </section>
    <section class="login-form">
      <div>
        <span class="eyebrow">WELCOME BACK</span>
        <h2>登录考勤工作台</h2>
        <p class="muted">使用管理员账号或分配给你的子账号。</p>
        <el-form @submit.prevent="submit" class="login-fields">
          <template v-if="!challenge">
            <el-form-item>
              <el-input
                v-model="username"
                :prefix-icon="User"
                autocomplete="username"
                placeholder="账号"
                size="large"
                @blur="loadMode"
              />
            </el-form-item>
            <el-form-item>
              <el-input
                v-model="password"
                :prefix-icon="Lock"
                type="password"
                show-password
                autocomplete="current-password"
                placeholder="密码"
                size="large"
              />
            </el-form-item>
            <el-form-item>
              <div class="verification-row">
                <el-input
                  v-model="code"
                  :prefix-icon="Key"
                  :maxlength="6"
                  autocomplete="one-time-code"
                  :placeholder="isTotp ? '谷歌验证码' : '验证码'"
                  size="large"
                />
                <template v-if="!isTotp">
                  <img
                    v-if="captcha"
                    :src="captcha.captchaImage"
                    alt="图片验证码，点击刷新"
                    title="点击刷新"
                    width="144"
                    height="52"
                    style="cursor: pointer"
                    @click="loadCaptcha"
                  /><el-button v-else link @click="loadCaptcha">加载验证码</el-button>
                </template>
              </div>
            </el-form-item>
            <el-checkbox v-model="remember" class="remember-box">记住账号密码</el-checkbox>
          </template>
          <template v-if="challenge">
            <div class="totp-setup"><p>首次绑定：在 Google Authenticator 中扫描二维码，或手动输入密钥，然后输入6位动态码完成绑定。</p><img :src="challenge.qrImage" alt="谷歌验证器绑定二维码" width="220" height="220"/><el-input :model-value="challenge.secret" readonly/><p class="muted">请妥善保存密钥，绑定成功后不会再次显示。</p></div>
            <el-form-item label="谷歌动态验证码（6位数字）"><el-input v-model="code" :maxlength="6" autocomplete="one-time-code" size="large"/></el-form-item>
            <el-button link :disabled="busy" @click="restart">返回重新登录</el-button>
          </template>
          <el-alert
            v-if="error"
            :title="error"
            type="error"
            :closable="false"
          /><el-button
            native-type="submit"
            type="primary"
            size="large"
            :loading="busy"
            style="width: 100%; margin-top: 20px"
            >{{challenge ? '验证并登录 →' : '登录 →'}}</el-button
          ></el-form
        >
        <p class="muted" style="font-size: 11px; margin-top: 22px">
          免费服务休眠后，首次连接可能需要约一分钟。
        </p>
      </div>
    </section>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Key, Lock, User } from "@element-plus/icons-vue";
import { useRouter } from "vue-router";
import request from "../../api/request";
import { useUserStore } from "../../store/user";
const username = ref(""),
  password = ref(""),
  error = ref(""),
  busy = ref(false),
  router = useRouter(),
  user = useUserStore();
const challenge = ref<any>(null),
  captcha = ref<any>(null),
  code = ref(""),
  remember = ref(false),
  mode = ref<"captcha" | "totp">("captcha");
const isTotp = computed(() => mode.value === "totp");
const REMEMBER_KEY = "tg_login_remember";
function readRemembered() {
  try {
    const raw = localStorage.getItem(REMEMBER_KEY);
    if (!raw) return;
    const v = JSON.parse(raw);
    if (typeof v?.u === "string" && typeof v?.p === "string") {
      username.value = v.u;
      password.value = v.p;
      remember.value = true;
    }
  } catch {}
}
function saveRemembered() {
  try {
    if (remember.value) {
      localStorage.setItem(
        REMEMBER_KEY,
        JSON.stringify({ u: username.value, p: password.value }),
      );
    } else {
      localStorage.removeItem(REMEMBER_KEY);
    }
  } catch {}
}
let modeSeq = 0;
async function loadMode() {
  const name = username.value.trim();
  const seq = ++modeSeq;
  let next: "captcha" | "totp" = "captcha";
  if (name) {
    try {
      const r: any = await request.get("/auth/login-mode", {
        params: { username: name },
      });
      if (r.data?.mode === "totp") next = "totp";
    } catch {}
  }
  if (seq !== modeSeq) return;
  mode.value = next;
  if (next === "captcha" && !captcha.value) loadCaptcha();
}
async function loadCaptcha() {
  try {
    const r: any = await request.get("/auth/captcha");
    captcha.value = r.data;
  } catch (e: any) {
    captcha.value = null;
    error.value = e.message;
  }
}
function restart() {
  challenge.value = null;
  code.value = "";
  error.value = "";
  password.value = "";
  loadCaptcha();
}
async function submit() {
  busy.value = true;
  error.value = "";
  try {
    if (challenge.value) {
      await user.verifyLogin({
        challengeId: challenge.value.challengeId,
        code: code.value,
      });
      challenge.value = null;
      code.value = "";
      await router.push("/");
    } else {
      const r = await user.login({
        username: username.value,
        password: password.value,
        captchaId: isTotp.value ? "" : captcha.value?.captchaId || "",
        code: code.value,
      });
      saveRemembered();
      password.value = "";
      code.value = "";
      if (r?.verification === "setup") {
        // First-time Google Authenticator binding: show the QR code.
        challenge.value = r;
      } else {
        await router.push("/");
      }
    }
  } catch (e: any) {
    error.value = e.message;
    if (!challenge.value) {
      code.value = "";
      if (!isTotp.value) loadCaptcha();
    }
  } finally {
    busy.value = false;
  }
}
onMounted(() => {
  readRemembered();
  loadCaptcha();
  if (username.value) loadMode();
});
</script>
<style scoped>
.verification-row{display:flex;align-items:center;gap:12px;width:100%}.verification-row img{flex-shrink:0;border-radius:8px}.login-fields :deep(.el-form-item){margin-bottom:18px}.remember-box{margin:-4px 0 4px}.totp-setup{text-align:center}.totp-setup p{font-size:13px;line-height:1.7}.totp-setup>img{display:block;margin:12px auto}

.login {
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  min-height: 100vh;
}
.login-story {
  background: #102b2d;
  color: #e0eee6;
  padding: 65px 9%;
  position: relative;
  overflow: hidden;
}
.eyebrow {
  font-size: 10px;
  letter-spacing: 2px;
  color: #879e96;
}
.login-story h1 {
  font-size: clamp(28px, 3vw, 46px);
  line-height: 1.5;
  font-weight: 500;
  margin-top: 100px;
}
.login-story p {
  font-size: 12px;
  color: #9ab8ad;
  line-height: 2;
}
.orbit {
  margin: 65px auto;
  border: 1px solid #31514a;
  width: 230px;
  height: 230px;
  border-radius: 50%;
  display: grid;
  place-content: center;
  text-align: center;
  outline: 1px solid #24423d;
  outline-offset: 30px;
}
.orbit span {
  font-size: 65px;
  color: #c5e9b8;
}
.orbit small {
  font-size: 8px;
  letter-spacing: 2px;
  color: #789d8a;
  margin-top: 15px;
}
.story-footer {
  font-size: 11px;
  color: #729286;
  margin-top: 80px;
}
.login-form {
  display: grid;
  place-items: center;
  background: #fbfcfb;
  padding: 40px;
}
.login-form > div {
  width: 100%;
  max-width: 350px;
}
.login-form h2 {
  font-size: 27px;
  margin: 20px 0 10px;
}
.login-form form {
  margin-top: 35px;
}
@media (max-width: 760px) {
  .login {
    grid-template-columns: 1fr;
  }
  .login-story {
    display: none;
  }
  .login-form {
    min-height: 100vh;
  }
}
</style>

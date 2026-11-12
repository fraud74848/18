import { createApp } from "vue";
import { createPinia } from "pinia";
import ElementPlus from "element-plus";
import zhCn from "element-plus/dist/locale/zh-cn.mjs";
import "element-plus/dist/index.css";
import * as icons from "@element-plus/icons-vue";
import App from "./App.vue";
import router from "./router";
import { setupPermissionDirective } from "./store/permission";
const app = createApp(App);
app.use(createPinia());
app.use(router);
app.use(ElementPlus, { locale: zhCn });
for (const [key, component] of Object.entries(icons))
  app.component(key, component);
setupPermissionDirective(app);
app.mount("#app");

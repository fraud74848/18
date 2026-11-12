import { defineStore } from "pinia";
import { ref } from "vue";
import request from "../api/request";
export const useUserStore = defineStore("user", () => {
  const role = ref(""),
    username = ref(""),
    permissions = ref<string[]>([]),
    isAuthenticated = ref(false),
    userId = ref("");
  const token = ref("");
  function apply(data: any) {
    role.value = data.role;
    username.value = data.username;
    userId.value = data.id;
    permissions.value = data.permissions || [];
    isAuthenticated.value = true;
  }
  async function login(payload: any) {
    const r: any = await request.post("/auth/login", payload);
    if(!r.data.verification)apply(r.data);
    return r.data;
  }
  async function verifyLogin(payload:any){const r:any=await request.post('/auth/verify',payload);apply(r.data)}
  async function validateSession() {
    try {
      const r: any = await request.get("/auth/me");
      apply(r.data);
      return true;
    } catch {
      isAuthenticated.value = false;
      return false;
    }
  }
  async function logout() {
    await request.post("/auth/logout");
    role.value = "";
    isAuthenticated.value = false;
    location.assign("/login");
  }
  function hasPermission(p: string) {
    if(role.value==='admin')return true;
    if(!permissions.value.includes(p))return false;
    const [module,action]=p.split(':');
    return module==='data'||action==='view'||permissions.value.includes(module+':view');
  }
  return {
    role,
    username,
    permissions,
    isAuthenticated,
    userId,
    token,
    login,
    verifyLogin,
    logout,
    validateSession,
    ensureSession: validateSession,
    hasPermission,
    hasAnyPermission: (p: string[]) => p.some(hasPermission),
  };
});

// Shared by menu, routes and saved workspace tabs.
export const pagePermissions: Record<string,string> = {
 '/':'overview:view','/accounts':'accounts:view','/roles':'roles:view',
 '/telegram':'telegram:view','/attendance/telegram':'telegram:view',
 '/telegram-records':'records:view','/telegram-punch':'punch:view','/telegram-punch-popup':'punch:view',
 '/attendance':'attendance:view','/attendance-review':'attendance_review:view','/attendance/review':'attendance_review:view',
 '/penalty-details':'penalty:view','/attendance/penalty':'penalty:view','/attendance/performance':'performance:view',
 '/memory':'maintenance:view'
};
export function canOpenPage(path:string) {
 const user=useUserStore();
 return user.isAuthenticated && (path==='/password' || !!pagePermissions[path] && user.hasPermission(pagePermissions[path]));
}

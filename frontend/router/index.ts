import { createRouter, createWebHistory } from "vue-router";
import { useUserStore, canOpenPage } from "../store/user";
const router = createRouter({
  history: createWebHistory(),
  routes: [
    {path:"/roles",component:()=>import("../views/standalone/Accounts.vue")},
    {path:"/password",component:()=>import("../views/standalone/Accounts.vue")},
    {path:"/memory",component:()=>import("../views/standalone/Accounts.vue")},
    {path:"/accounts",component:()=>import("../views/standalone/Accounts.vue")},
    {
      path: "/login",
      component: () => import("../views/standalone/Login.vue"),
    },
    { path: "/", component: () => import("../views/standalone/Overview.vue") },
    {
      path: "/employees",
      redirect: "/attendance",
    },
    {
      path: "/telegram",
      alias: "/attendance/telegram",
      component: () => import("../views/attendance/TelegramAttendance.vue"),
    },
    {
      path: "/telegram-records",
      component: () => import("../views/attendance/TelegramRecords.vue"),
    },
    {
      path: "/telegram-punch",
      component: () => import("../views/attendance/TelegramActivityPunch.vue"),
    },
    {
      path: "/attendance",
      component: () => import("../views/attendance/AttendanceRecords.vue"),
    },
    {
      path: "/attendance-review",
      alias: "/attendance/review",
      component: () => import("../views/attendance/AttendanceReview.vue"),
    },
    {
      path: "/penalty-details",
      alias: "/attendance/penalty",
      component: () => import("../views/attendance/PenaltyDetails.vue"),
    },
    {
      path: "/telegram-punch-popup",
      name: "TelegramActivityPunchPopup",
      meta: { popup: true },
      component: () => import("../views/attendance/TelegramActivityPunch.vue"),
    },
    {
      path: "/attendance/performance",
      component: () => import("../views/attendance/PerformanceReview.vue"),
    },
    { path: "/:pathMatch(.*)*", redirect: "/" },
  ],
});
router.beforeEach(async (to) => {
  if (to.path === "/login") return true;
  const user = useUserStore();
  if (!(await user.validateSession())) return "/login";
  if (!canOpenPage(to.path)) return "/password";
  return true;
});
export default router;

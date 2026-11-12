// frontend/src/store/permission.ts
import { Directive, App } from "vue";
import { useUserStore } from "@store/user";

/**
 * 检查用户是否拥有指定权限
 */
function hasPermission(permission: string): boolean {
  const userStore = useUserStore();

  if (userStore.role === "admin") {
    return true;
  }

  return userStore.hasPermission(permission);
}

export function hasAnyPermission(permissions: string[]): boolean {
  const userStore = useUserStore();

  if (userStore.role === "admin") {
    return true;
  }

  return permissions.some((p) => userStore.hasPermission(p));
}

export function hasAllPermissions(permissions: string[]): boolean {
  const userStore = useUserStore();

  if (userStore.role === "admin") {
    return true;
  }

  return permissions.every((p) => userStore.hasPermission(p));
}

export const permissionDirective: Directive = {
  mounted(el, binding) {
    if (!el) return;
    applyPermissionVisibility(el, binding.value);
  },

  updated(el, binding) {
    if (!el) return;
    applyPermissionVisibility(el, binding.value);
  },
};

function applyPermissionVisibility(el: HTMLElement, value: unknown) {
  if (!value) return;

  let hasPerm = false;
  if (typeof value === "string") {
    hasPerm = hasPermission(value);
  } else if (Array.isArray(value)) {
    hasPerm = hasAnyPermission(value);
  } else {
    return;
  }

  // Hide instead of removeChild — removing Vue-managed nodes during
  // transition/keep-alive remounts can blank the main content area.
  if (hasPerm) {
    if (el.dataset.permDisplay !== undefined) {
      el.style.display = el.dataset.permDisplay;
      delete el.dataset.permDisplay;
    } else {
      el.style.removeProperty("display");
    }
  } else {
    if (el.dataset.permDisplay === undefined) {
      el.dataset.permDisplay = el.style.display || "";
    }
    el.style.display = "none";
  }
}

export function setupPermissionDirective(app: App) {
  app.directive("permission", permissionDirective);
}

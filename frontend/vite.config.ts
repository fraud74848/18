import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { resolve } from "path";
import type { RollupLog, LoggingFunction } from "rollup";

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      "@": resolve(__dirname),
      "@api": resolve(__dirname, "api"),
      "@store": resolve(__dirname, "store"),
      "@views": resolve(__dirname, "views"),
      "@layouts": resolve(__dirname, "layouts"),
      "@router": resolve(__dirname, "router"),
    },
  },
  css: {
    preprocessorOptions: {
      scss: {
        api: "modern-compiler",
      },
      sass: {
        api: "modern-compiler",
      },
    },
  },
  build: {
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      onwarn(warning: RollupLog, warn: LoggingFunction) {
        // Element Plus / Vue 产物里偶发无法识别的 /* #__PURE__ */ 标注
        if (warning.code === "INVALID_ANNOTATION") {
          return;
        }
        warn(warning);
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api/v1": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
      "/ws": {
        target: "http://127.0.0.1:8080",
        ws: true,
        changeOrigin: true,
        secure: false,
      },
    },
  },
  preview: {
    port: 5173,
    proxy: {
      "/api/v1": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
      "/ws": {
        target: "http://127.0.0.1:8080",
        ws: true,
        changeOrigin: true,
        secure: false,
      },
    },
  },
});

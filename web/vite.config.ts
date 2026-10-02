import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
// 从 vitest 引 defineConfig：它比 vite 的多认识 test 这一段配置。
import { defineConfig } from 'vitest/config'

// 后端默认监听地址。开发态前端与后端不同端口，靠这里的代理把请求转过去，
// 因此前端代码里永远只写同源路径，不需要区分开发与生产。
const backend = process.env.CLOUDTRACE_BACKEND ?? 'http://127.0.0.1:17443'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    // 产物直接落在 web/dist，由 go:embed 打进二进制。
    outDir: 'dist',
    rollupOptions: {
      // 两个入口：主应用与登录页。登录页独立打包，未登录时不必把整个前端
      // 加载进来——那是局域网用户最先看到的一屏。
      input: {
        main: fileURLToPath(new URL('./index.html', import.meta.url)),
        login: fileURLToPath(new URL('./login.html', import.meta.url)),
      },
    },
    // 每次构建前清空：上一版留下的旧 chunk 会被一起嵌进二进制，
    // 白白增大体积，还会让人以为旧代码还在跑。
    emptyOutDir: true,
    // 单文件体积上限调高一点：这个应用不需要按路由拆包，
    // 拆出来的 chunk 只会让首屏多几次请求。
    chunkSizeWarningLimit: 800,
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // WebSocket 必须显式开 ws，否则握手会被当成普通 HTTP 请求。
      '/ws': { target: backend, ws: true },
      '/api': { target: backend },
      '/auth': { target: backend },
      // 本地结果地址：开发态也让它走代理，前端才不用写死端口。
      '/latest': { target: backend },
      '/latest.json': { target: backend },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.spec.ts'],
    // 组件测试需要 DOM，jsdom 之外的部分保持与浏览器一致即可。
    restoreMocks: true,
  },
})

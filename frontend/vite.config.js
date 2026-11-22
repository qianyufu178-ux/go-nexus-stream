import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发模式把 /api 与 /ws 代理到本地 Nexus-Core（默认 :8080），
// 这样 `npm run dev` 与打包后的同源部署共用同一套前端代码。
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://127.0.0.1:8080',
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
})

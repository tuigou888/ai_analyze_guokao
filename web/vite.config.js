import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 产物直接输出到 Go 的嵌入目录，省掉一步手工拷贝。
// 前端与后端同源部署，因此 API 一律用相对路径，不涉及 CORS。
export default defineConfig({
  plugins: [vue(), {
    name: 'keep-go-embed-placeholder',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' })
    },
  }],
  build: {
    outDir: '../cmd/gk/web_dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // 必须与 `make serve` 的监听端口一致：5173 是 Vite 自己的端口，
    // 8080 常被其他程序占用，后端默认改到 8081。
    // 保留浏览器 Host，后端据此校验完整 Origin；不能通过忽略端口来放行。
    proxy: {
      '/api': { target:'http://127.0.0.1:8081',changeOrigin:false },
      '/media': { target:'http://127.0.0.1:8081',changeOrigin:false },
    },
  },
})

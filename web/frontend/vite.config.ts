import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const isWails = process.env.WAILS_BUILD === '1'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:9740',
    },
  },
  build: {
    outDir: isWails ? '../../cmd/desktop/frontend/dist' : '../../pkg/api/static',
    emptyOutDir: true,
  },
})

import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// Served by the Go server at the root of the admin subdomain
// (convention: fo3-adm.<your-domain>).
export default defineConfig({
  plugins: [react()],
  base: '/',
})

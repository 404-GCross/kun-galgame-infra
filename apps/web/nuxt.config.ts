import tailwindcss from '@tailwindcss/vite'

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',

  devtools: { enabled: true },

  modules: ['@nuxt/eslint', '@nuxt/icon', '@nuxt/image'],

  devServer: {
    host: '127.0.0.1',
    port: 9420,
  },

  // Frontend
  css: ['~/styles/index.css'],

  vite: {
    // @ts-expect-error
    plugins: [tailwindcss()],
  },

  runtimeConfig: {
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || 'http://localhost:9277/api/v1',
    },
  },
})

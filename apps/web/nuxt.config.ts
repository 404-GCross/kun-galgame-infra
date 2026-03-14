import tailwindcss from '@tailwindcss/vite'

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',

  devtools: { enabled: true },

  modules: [
    '@nuxt/image',
    '@nuxt/icon',
    '@nuxt/eslint',
    '@nuxtjs/color-mode',
    '@pinia/nuxt',
    'pinia-plugin-persistedstate/nuxt',
    'nuxt-schema-org',
    'nuxt-umami'
  ],

  devServer: {
    host: '127.0.0.1',
    port: 9420
  },

  // Frontend
  css: ['~/styles/index.css'],

  vite: {
    // @ts-expect-error
    plugins: [tailwindcss()]
  },

  umami: {
    id: process.env.KUN_VISUAL_NOVEL_FORUM_UMAMI_ID,
    host: 'https://stats.kungal.org/',
    autoTrack: true
  },

  runtimeConfig: {
    public: {
      apiBase:
        process.env.NUXT_PUBLIC_API_BASE || 'http://localhost:9277/api/v1'
    }
  }
})

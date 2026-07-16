import tailwindcss from '@tailwindcss/vite'

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',

  devtools: { enabled: false },

  extends: ['@kungal/ui-nuxt'],

  // Subtle global fade for route changes. In the /docs section the sidebar is
  // held by the layout, so only the content column (the page) runs this — the
  // nav stays put. `out-in` avoids overlap; the CSS lives in main.css.
  app: {
    pageTransition: { name: 'page', mode: 'out-in' },
    layoutTransition: { name: 'layout', mode: 'out-in' }
  },

  // This app owns its Tailwind entry. @kungal/ui-nuxt deliberately does not
  // inject tailwindcss.css (INTEGRATION §5), so the imports + @source scan live
  // in app/assets/css/main.css.
  css: ['~/assets/css/main.css'],

  modules: [
    '@nuxt/eslint',
    '@nuxtjs/color-mode',
    '@pinia/nuxt',
    'pinia-plugin-persistedstate/nuxt'
  ],

  devServer: {
    host: '127.0.0.1',
    port: 9430
  },

  pinia: {
    storesDirs: ['./store/**']
  },

  piniaPluginPersistedstate: {
    cookieOptions: {
      maxAge: 60 * 60 * 24 * 7,
      sameSite: 'strict'
    }
  },

  colorMode: {
    preference: 'system',
    fallback: 'light',
    globalName: '__NEXTMOE_DEV_COLOR_MODE__',
    componentName: 'ColorScheme',
    classPrefix: 'kun-',
    classSuffix: '-mode',
    storageKey: 'nextmoe-dev-color-mode'
  },

  vite: {
    // @ts-expect-error ts-expect-error
    plugins: [tailwindcss()]
  },

  runtimeConfig: {
    // Same-origin proxy target for /api/** (server/routes/api/[...path].ts).
    // The browser only ever talks to developer.nextmoe.dev; Nitro forwards
    // /api/** to the oauth service server-side so there is ZERO CORS. In
    // production set NUXT_OAUTH_API_BASE=http://oauth:9277 (docker service
    // name); local dev points it at the locally-running oauth binary.
    oauthApiBase: process.env.NUXT_OAUTH_API_BASE || 'http://127.0.0.1:19277'
  }
})

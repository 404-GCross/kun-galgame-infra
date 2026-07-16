import tailwindcss from '@tailwindcss/vite'

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',

  devtools: { enabled: false },

  extends: ['@kungal/ui-nuxt'],

  // This app owns its Tailwind entry. The old @kun/ui layer injected its
  // tailwindcss.css; @kungal/ui-nuxt deliberately does not (INTEGRATION §5),
  // so the imports + @source scan live in app/assets/css/main.css.
  css: ['~/assets/css/main.css'],

  modules: [
    '@nuxt/eslint',
    '@nuxtjs/color-mode',
    '@pinia/nuxt',
    'pinia-plugin-persistedstate/nuxt',
    'nuxt-schema-org'
  ],

  devServer: {
    host: '127.0.0.1',
    port: 9421
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
    globalName: '__KUNGALGAME_WIKI_COLOR_MODE__',
    componentName: 'ColorScheme',
    classPrefix: 'kun-',
    classSuffix: '-mode',
    storageKey: 'kungalgame-wiki-color-mode'
  },

  vite: {
    // @ts-expect-error ts-expect-error
    plugins: [tailwindcss()]
  },

  runtimeConfig: {
    // SSR runs inside the docker container, where the galgame surface (hosted
    // by the catalog service since wiki-retirement W3) + OAuth APIs are
    // reachable by their compose service names (catalog:9281 / oauth:9277),
    // NOT by the browser's host-port URLs. Set in docker:
    //   NUXT_API_BASE_SSR=http://catalog:9281/api
    //   NUXT_AUTH_API_BASE_SSR=http://oauth:9277/api/v1
    // Empty in local dev → the dual-base readers fall back to the public bases.
    apiBaseSsr: process.env.NUXT_API_BASE_SSR || '',
    authApiBaseSsr: process.env.NUXT_AUTH_API_BASE_SSR || '',
    public: {
      apiBase:
        process.env.KUN_GALGAME_WIKI_NUXT_PUBLIC_API_BASE ||
        'http://127.0.0.1:9281/api',
      authApiBase:
        process.env.KUN_GALGAME_WIKI_NUXT_PUBLIC_AUTH_API_BASE ||
        'http://127.0.0.1:9277/api/v1',
      oauthAuthorizeBase:
        process.env.KUN_GALGAME_WIKI_NUXT_PUBLIC_OAUTH_AUTHORIZE_BASE ||
        'http://127.0.0.1:9277/api/v1',
      oauthClientID:
        process.env.KUN_GALGAME_WIKI_NUXT_PUBLIC_OAUTH_CLIENT_ID ||
        'galgame-wiki-admin',
      oauthRedirectURI:
        process.env.KUN_GALGAME_WIKI_NUXT_PUBLIC_OAUTH_REDIRECT_URI ||
        'http://127.0.0.1:9421/auth/callback',
      oauthScope: 'profile',
      // image_service public CDN base. For dev points to the R2 custom
      // domain; in production override via env. Must NOT have a trailing slash.
      imageCdnBase:
        process.env.KUN_GALGAME_WIKI_NUXT_PUBLIC_IMAGE_CDN_BASE ||
        'https://image.kungal.iloveren.link'
    }
  }
})

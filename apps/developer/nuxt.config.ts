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
    layoutTransition: { name: 'layout', mode: 'out-in' },
    // Favicon set — temporarily shares kungal's marks (the static files copied
    // from apps/web/public); swap for NextMoe's own brand set once ready.
    head: {
      link: [
        { rel: 'icon', type: 'image/x-icon', href: '/favicon.ico' },
        {
          rel: 'icon',
          type: 'image/png',
          sizes: '32x32',
          href: '/favicon-32x32.png'
        },
        {
          rel: 'icon',
          type: 'image/png',
          sizes: '16x16',
          href: '/favicon-16x16.png'
        },
        { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' }
      ]
    }
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
    // ALSO reused server-side for the OAuth code/refresh exchange (CORS-free).
    oauthApiBase: process.env.NUXT_OAUTH_API_BASE || 'http://127.0.0.1:19277',

    // Confidential OAuth client secret — SERVER ONLY, never exposed to the
    // browser. Used by server/routes/auth/{exchange,refresh}.post.ts on the
    // /oauth/token calls. Empty in local dev unless you seed a dev client.
    oauthClientSecret: process.env.NUXT_OAUTH_CLIENT_SECRET || '',

    public: {
      // Browser-facing OAuth API base for the top-level /oauth/authorize
      // navigation (the IdP's PUBLIC origin — distinct from the server-only,
      // internal oauthApiBase above). Prod: https://oauth.kungal.com/api/v1.
      oauthAuthorizeBase:
        process.env.NUXT_PUBLIC_OAUTH_AUTHORIZE_BASE ||
        'http://127.0.0.1:9277/api/v1',
      // OP frontend origin, for the seamless register redirect (/auth/register).
      // Prod: https://oauth.kungal.com. Dev: the OP web app (:9420).
      oauthWebBase:
        process.env.NUXT_PUBLIC_OAUTH_WEB_BASE || 'http://127.0.0.1:9420',
      // This client's id + its registered callback. redirect_uri is
      // exact-string-matched by the IdP, so it MUST equal the registered value.
      oauthClientId: process.env.NUXT_PUBLIC_OAUTH_CLIENT_ID || '',
      oauthRedirectUri:
        process.env.NUXT_PUBLIC_OAUTH_REDIRECT_URI ||
        'http://127.0.0.1:9430/auth/callback'
    }
  }
})

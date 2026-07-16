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
    'nuxt-schema-org',
    'nuxt-echarts'
  ],

  // Tree-shaken ECharts: only the bar chart + the components the registration
  // dashboard uses are bundled. <VChart> is auto-imported and client-rendered.
  echarts: {
    renderer: 'canvas',
    charts: ['BarChart'],
    components: ['GridComponent', 'TooltipComponent', 'MarkLineComponent']
  },

  devServer: {
    host: '127.0.0.1',
    port: 9420
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
    globalName: '__KUNGALGAME_COLOR_MODE__',
    componentName: 'ColorScheme',
    classPrefix: 'kun-',
    classSuffix: '-mode',
    storageKey: 'kungalgame-color-mode'
  },

  vite: {
    // @ts-expect-error ts-expect-error
    plugins: [tailwindcss()]
  },

  runtimeConfig: {
    // SSR runs inside the docker container, where the OAuth API is reachable by
    // its compose service name (oauth:9277), NOT by the browser's host-port URL.
    // Set NUXT_API_BASE_SSR=http://oauth:9277/api/v1 in docker; empty in local
    // dev (the dual-base reader falls back to public.apiBase).
    apiBaseSsr: process.env.NUXT_API_BASE_SSR || '',
    // Same dual-base split for the catalog service (a separate binary on its
    // own port). Set NUXT_CATALOG_API_BASE_SSR=http://catalog:9281/api/v1 in
    // docker; empty in local dev.
    catalogApiBaseSsr: process.env.NUXT_CATALOG_API_BASE_SSR || '',
    // Same dual-base split for the Trust & Safety service (a separate binary on
    // its own port). Set NUXT_TRUST_API_BASE_SSR=http://trust:9283/api/v1 in
    // docker; empty in local dev.
    trustApiBaseSsr: process.env.NUXT_TRUST_API_BASE_SSR || '',
    // Same dual-base split for the AI-gateway (a separate binary on its own
    // port). Set NUXT_AI_API_BASE_SSR=http://ai:9284/api/v1 in docker; empty in
    // local dev.
    aiApiBaseSsr: process.env.NUXT_AI_API_BASE_SSR || '',
    public: {
      apiBase:
        process.env.KUN_VISUAL_NOVEL_NUXT_PUBLIC_API_BASE ||
        'http://127.0.0.1:9277/api/v1',
      // catalog service base for the BROWSER. The catalog service has no
      // public domain (docker-network only), so the default is the
      // same-origin /catalog-proxy server route, which relays admin calls to
      // catalogApiBaseSsr. Works unchanged in local dev (the relay falls back
      // to 127.0.0.1:9281).
      catalogApiBase:
        process.env.KUN_VISUAL_NOVEL_NUXT_PUBLIC_CATALOG_API_BASE ||
        '/catalog-proxy',
      // trust service base for the BROWSER. The trust service has no public
      // domain (docker-network only), so the default is the same-origin
      // /trust-proxy server route, which relays admin calls to
      // trustApiBaseSsr. Works unchanged in local dev (the relay falls back
      // to 127.0.0.1:9283).
      trustApiBase:
        process.env.KUN_VISUAL_NOVEL_NUXT_PUBLIC_TRUST_API_BASE ||
        '/trust-proxy',
      // AI-gateway base for the BROWSER. The AI service has no public domain
      // (docker-network only), so the default is the same-origin /ai-proxy
      // server route, which relays admin calls to aiApiBaseSsr. Works unchanged
      // in local dev (the relay falls back to 127.0.0.1:9284).
      aiApiBase:
        process.env.KUN_VISUAL_NOVEL_NUXT_PUBLIC_AI_API_BASE || '/ai-proxy',
      // image_service public CDN base; override via env in prod.
      imageCdnBase:
        process.env.KUN_VISUAL_NOVEL_NUXT_PUBLIC_IMAGE_CDN_BASE ||
        'https://image.kungal.iloveren.link'
    }
  }
})

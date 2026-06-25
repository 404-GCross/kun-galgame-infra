<script setup lang="ts">
// Public app-directory ("生态一键登录") strip shown below the register/login
// card: "拥有鲲 Galgame 账号，一键登录以下网站" + each opt-in (listed) app's
// logo + name. Pure presentation, off the auth path — fetches the public
// GET /oauth/ecosystem and renders nothing when the list is empty.
// Contract: docs/integration/oauth/10-app-directory.md.
import type { EcosystemApp } from '~~/shared/types/oauth-client'

// SSR-friendly read; the endpoint is public so no token is required. The
// envelope is unwrapped to `{ apps }` by useApiFetch's transform (null on a
// non-zero code), so coalesce to an empty list.
const { data } = await useApiFetch<{ apps: EcosystemApp[] }>('/oauth/ecosystem')

const apps = computed(() => data.value?.apps ?? [])
</script>

<template>
  <div v-if="apps.length" class="mt-6 text-center">
    <p class="text-xs text-default-400">
      拥有鲲 Galgame 账号，一键登录以下网站
    </p>
    <div class="mt-3 flex flex-wrap items-center justify-center gap-4">
      <KunTooltip
        v-for="app in apps"
        :key="app.site_domain || app.name"
        :text="app.tagline || app.name"
      >
        <div class="flex flex-col items-center gap-1">
          <KunImage
            v-if="app.logo_url"
            :src="app.logo_url"
            :alt="app.name"
            :width="32"
            :height="32"
            object-fit="contain"
            class-name="size-8 rounded-md"
          />
          <span
            v-else
            class="flex size-8 items-center justify-center rounded-md bg-default-100 text-xs font-medium text-default-500"
          >
            {{ app.name.slice(0, 1) }}
          </span>
          <span class="max-w-20 truncate text-xs text-default-500">
            {{ app.name }}
          </span>
        </div>
      </KunTooltip>
    </div>
  </div>
</template>

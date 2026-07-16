<script setup lang="ts">
import { DEV_TIER_COLORS, devTierLimitHint } from '~/constants/devapi'
import type { DevApp } from '~~/shared/types/devapi'

// Developer-platform app list. SSR-rendered like the OAuth-clients page. Two
// sources: the dev_enabled apps (management surface) and the full OAuth-client
// list (the enable-flow candidate pool — reused from the OAuth-clients data
// path per 裁定 2).
const { data: appsData, status, refresh } =
  await useApiFetch<DevApp[]>('/admin/devapi/apps')
const { data: clientsData, refresh: refreshClients } =
  await useApiFetch<OAuthClient[]>('/oauth/clients')

const apps = computed(() => appsData.value ?? [])
const clients = computed(() => clientsData.value ?? [])
const isLoading = computed(() => status.value === 'pending')

// Enable candidates = OAuth clients that are not already dev_enabled.
const enabledIds = computed(() => new Set(apps.value.map((a) => a.client_id)))
const candidates = computed(() =>
  clients.value.filter((c) => !enabledIds.value.has(c.id))
)

const showEnableModal = ref(false)
const editingApp = ref<DevApp | null>(null)

const refreshAll = () => {
  refresh()
  refreshClients()
}

const handleEnabled = () => {
  showEnableModal.value = false
  refreshAll()
}

const handleUpdated = () => {
  editingApp.value = null
  refreshAll()
}

const ownerLabel = (app: DevApp) =>
  app.owner_user_id ? `#${app.owner_user_id}` : '未指定'

const overrideLabel = (app: DevApp) => {
  const rate = app.dev_rate_per_min > 0 ? `${app.dev_rate_per_min} 次/分` : null
  const quota = app.dev_quota_daily > 0 ? `${app.dev_quota_daily} 次/日` : null
  if (!rate && !quota) return null
  return [rate, quota].filter(Boolean).join(' · ')
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">开发者平台</h1>
        <p class="mt-1 text-default-500">管理 NextMoe 开放 API 的应用、等级与密钥</p>
      </div>
      <KunButton color="primary" class-name="shrink-0 self-start" @click="showEnableModal = true">
        <KunIcon name="lucide:plus" class="mr-2 size-4" />
        启用新应用
      </KunButton>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <KunIcon name="lucide:loader-circle" class="size-8 animate-spin text-primary" />
    </div>

    <div v-else-if="apps.length === 0" class="rounded-xl bg-content1 py-12 text-center shadow-sm">
      <KunIcon name="lucide:terminal" class="mx-auto mb-4 size-12 text-default-200" />
      <p class="text-default-400">尚无已启用的开发者应用</p>
      <p class="mt-1 text-sm text-default-300">从既有 OAuth 客户端启用一个以接入开放 API</p>
    </div>

    <div v-else class="space-y-4">
      <KunCard
        v-for="app in apps"
        :key="app.client_id"
        content-class="justify-start gap-0"
        class-name="p-6"
      >
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="text-lg font-semibold text-foreground">{{ app.name }}</h3>
              <KunChip :color="DEV_TIER_COLORS[app.dev_tier] ?? 'default'" variant="flat" size="sm">
                {{ app.dev_tier }}
              </KunChip>
            </div>
            <p class="mt-1 truncate font-mono text-sm text-default-400">{{ app.client_id }}</p>
          </div>
          <div class="flex shrink-0 gap-1">
            <KunButton variant="flat" size="sm" @click="editingApp = app">
              <KunIcon name="lucide:sliders-horizontal" class="mr-1 size-4" />
              配置
            </KunButton>
            <KunButton
              color="primary"
              variant="flat"
              size="sm"
              @click="navigateTo(`/devapi/${app.client_id}`)"
            >
              <KunIcon name="lucide:key-round" class="mr-1 size-4" />
              密钥 ({{ app.key_count }})
            </KunButton>
          </div>
        </div>

        <div class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div>
            <p class="text-xs text-default-400">归属用户</p>
            <p class="mt-0.5 text-sm text-foreground">{{ ownerLabel(app) }}</p>
          </div>
          <div>
            <p class="text-xs text-default-400">密钥数</p>
            <p class="mt-0.5 text-sm text-foreground">{{ app.key_count }}</p>
          </div>
          <div class="col-span-2">
            <p class="text-xs text-default-400">限额</p>
            <p class="mt-0.5 text-sm text-foreground">
              {{ overrideLabel(app) ?? `默认（${devTierLimitHint(app.dev_tier)}）` }}
            </p>
          </div>
        </div>
      </KunCard>
    </div>

    <DevapiEnableModal
      v-if="showEnableModal"
      :candidates="candidates"
      @close="showEnableModal = false"
      @enabled="handleEnabled"
    />

    <DevapiConfigModal
      v-if="editingApp"
      :app="editingApp"
      @close="editingApp = null"
      @updated="handleUpdated"
    />
  </div>
</template>

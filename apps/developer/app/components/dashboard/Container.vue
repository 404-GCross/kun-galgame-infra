<script setup lang="ts">
import { DEV_TIER_COLORS, MAX_APPS_PER_ACCOUNT } from '~/constants/dev'
import type { DevApp } from '~~/shared/types/dev'

// My applications. SSR-rendered via useApiFetch (access_token cookie forwarded);
// the create flow POSTs then refreshes the list.
useSeoMeta({ title: '控制台', robots: 'noindex' })

const { data: appsData, refresh } = await useApiFetch<DevApp[]>('/dev/apps')
const apps = computed(() => appsData.value ?? [])

const showCreate = ref(false)
const atLimit = computed(() => apps.value.length >= MAX_APPS_PER_ACCOUNT)

const handleCreated = () => {
  showCreate.value = false
  refresh()
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold text-foreground">我的应用</h1>
        <p class="mt-1 text-sm text-default-500">
          管理你的开放 API 应用与密钥 · 每个账号最多
          {{ MAX_APPS_PER_ACCOUNT }} 个应用
        </p>
      </div>
      <KunButton
        color="primary"
        :disabled="atLimit"
        @click="showCreate = true"
      >
        <KunIcon name="lucide:plus" class="mr-1 size-4" />
        创建应用
      </KunButton>
    </div>

    <div
      v-if="atLimit"
      class="rounded-lg bg-warning-50 p-3 text-sm text-warning"
    >
      <KunIcon name="lucide:info" class="mr-1 inline size-4" />
      已达到应用数量上限（{{ MAX_APPS_PER_ACCOUNT }}）。如需更多，请停用不再使用的应用。
    </div>

    <!-- App list -->
    <div v-if="apps.length" class="grid gap-4 sm:grid-cols-2">
      <NuxtLink
        v-for="app in apps"
        :key="app.client_id"
        :to="`/apps/${app.client_id}`"
        class="group"
      >
        <KunCard
          :is-hoverable="true"
          content-class="justify-start gap-0 items-start"
          class-name="p-5 h-full"
        >
          <div class="flex w-full items-start justify-between gap-3">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-base font-semibold text-foreground">
                {{ app.name }}
              </h2>
              <KunChip
                :color="DEV_TIER_COLORS[app.tier] ?? 'default'"
                variant="flat"
                size="xs"
              >
                {{ app.tier }}
              </KunChip>
            </div>
            <KunIcon
              name="lucide:arrow-up-right"
              class="size-4 shrink-0 text-default-300 transition-colors group-hover:text-primary"
            />
          </div>

          <p
            v-if="app.description"
            class="mt-1 line-clamp-2 text-sm text-default-500"
          >
            {{ app.description }}
          </p>

          <p class="mt-3 truncate font-mono text-xs text-default-400">
            {{ app.client_id }}
          </p>

          <div class="mt-3 flex items-center gap-4 text-xs text-default-400">
            <span class="flex items-center gap-1">
              <KunIcon name="lucide:key-round" class="size-3.5" />
              {{ app.key_count }} 个密钥
            </span>
            <span class="flex items-center gap-1">
              <KunIcon name="lucide:calendar" class="size-3.5" />
              {{ formatDate(app.created_at, { isShowYear: true }) }}
            </span>
          </div>
        </KunCard>
      </NuxtLink>
    </div>

    <!-- Empty state -->
    <KunCard v-else content-class="p-12" class-name="border-dashed">
      <div class="text-center">
        <div
          class="mx-auto flex size-12 items-center justify-center rounded-full bg-default-100"
        >
          <KunIcon name="lucide:boxes" class="size-6 text-default-400" />
        </div>
        <p class="mt-4 font-medium text-foreground">还没有应用</p>
        <p class="mt-1 text-sm text-default-500">
          创建第一个应用,即可领取密钥并调用开放 API。
        </p>
        <KunButton color="primary" class="mt-4" @click="showCreate = true">
          <KunIcon name="lucide:plus" class="mr-1 size-4" />
          创建应用
        </KunButton>
      </div>
    </KunCard>

    <DashboardCreateAppModal
      v-if="showCreate"
      @close="showCreate = false"
      @created="handleCreated"
    />
  </div>
</template>

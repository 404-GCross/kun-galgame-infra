<script setup lang="ts">
import { DEV_TIER_COLORS, DEV_TIER_LABELS } from '~/constants/dev'
import type { DevApp, DevKey, DevKeyMinted, DevUsageDayFace } from '~~/shared/types/dev'

const route = useRoute()
const clientId = computed(() => String(route.params.clientId))

useSeoMeta({ title: '应用详情', robots: 'noindex' })

const api = useApi()

const { data: appData, refresh: refreshApp } = await useApiFetch<DevApp>(
  () => `/dev/apps/${clientId.value}`
)
const { data: keysData, refresh: refreshKeys } = await useApiFetch<DevKey[]>(
  () => `/dev/apps/${clientId.value}/keys`
)
const { data: usageData } = await useApiFetch<DevUsageDayFace[]>(
  () => `/dev/apps/${clientId.value}/usage?days=7`
)

const app = computed(() => appData.value)
const keys = computed(() => keysData.value ?? [])
const usage = computed(() => usageData.value ?? [])

const busy = ref(false)
const showEditModal = ref(false)
const showMintModal = ref(false)

const mintedKey = ref<DevKeyMinted | null>(null)
const revealRotated = ref(false)

const confirmDialog = ref<{
  title: string
  body: string
  danger?: boolean
  run: () => Promise<void>
} | null>(null)

const runConfirm = async () => {
  if (!confirmDialog.value) return
  busy.value = true
  try {
    await confirmDialog.value.run()
  } finally {
    busy.value = false
    confirmDialog.value = null
  }
}

const limitLabel = (v: number, unit: string) =>
  v > 0 ? `${v.toLocaleString()} ${unit}` : '不限'

const handleMinted = (minted: DevKeyMinted) => {
  showMintModal.value = false
  revealRotated.value = false
  mintedKey.value = minted
  refreshKeys()
  refreshApp()
}

const closeReveal = () => {
  mintedKey.value = null
}

const handleEdited = () => {
  showEditModal.value = false
  refreshApp()
}

const askRotate = (key: DevKey) => {
  confirmDialog.value = {
    title: '轮换密钥',
    body: `将为「${key.name}」生成一枚新密钥，旧密钥进入 72 小时宽限后失效。请确保有机会保存新密钥。`,
    run: async () => {
      const res = await api.post<DevKeyMinted>(
        `/dev/apps/${clientId.value}/keys/${key.id}/rotate`
      )
      if (res.code === 0 && res.data) {
        revealRotated.value = true
        mintedKey.value = res.data
        refreshKeys()
      } else {
        useKunMessage(res.message || '轮换失败', 'error')
      }
    }
  }
}

const askRevoke = (key: DevKey) => {
  confirmDialog.value = {
    title: '吊销密钥',
    body: `吊销「${key.name}」后立即生效且不可撤销，使用该密钥的所有请求将被拒绝。确认继续？`,
    danger: true,
    run: async () => {
      const res = await api.delete(
        `/dev/apps/${clientId.value}/keys/${key.id}`
      )
      if (res.code === 0) {
        useKunMessage('密钥已吊销', 'success')
        refreshKeys()
        refreshApp()
      } else {
        useKunMessage(res.message || '吊销失败', 'error')
      }
    }
  }
}

const askDeactivate = () => {
  confirmDialog.value = {
    title: '停用应用',
    body: '停用后该应用退出开放 API 平台，其所有密钥将立即失效。确认继续？',
    danger: true,
    run: async () => {
      const res = await api.delete(`/dev/apps/${clientId.value}`)
      if (res.code === 0) {
        useKunMessage('应用已停用', 'success')
        navigateTo('/dashboard')
      } else {
        useKunMessage(res.message || '停用失败', 'error')
      }
    }
  }
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center gap-2">
      <KunButton
        variant="light"
        size="sm"
        is-icon-only
        aria-label="返回控制台"
        @click="navigateTo('/dashboard')"
      >
        <KunIcon name="lucide:arrow-left" class="size-5" />
      </KunButton>
      <h1 class="text-2xl font-bold text-foreground">应用详情</h1>
    </div>

    <KunCard v-if="!app" content-class="p-10">
      <p class="text-center text-default-400">
        未找到该应用（可能已停用或不存在）。
      </p>
    </KunCard>

    <template v-else>
      <KunCard content-class="justify-start gap-0" class-name="p-6">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-lg font-semibold text-foreground">
                {{ app.name }}
              </h2>
              <KunChip
                :color="DEV_TIER_COLORS[app.tier] ?? 'default'"
                variant="flat"
                size="sm"
              >
                {{ DEV_TIER_LABELS[app.tier] ?? app.tier }}
              </KunChip>
            </div>
            <p v-if="app.description" class="mt-1 text-sm text-default-500">
              {{ app.description }}
            </p>
            <div class="mt-2 flex items-center gap-2">
              <p class="truncate font-mono text-sm text-default-400">
                {{ app.client_id }}
              </p>
              <KunCopy :text="app.client_id" size="sm" />
            </div>
          </div>
          <div class="flex shrink-0 gap-1">
            <KunButton variant="flat" size="sm" @click="showEditModal = true">
              <KunIcon name="lucide:pencil" class="mr-1 size-4" />
              编辑
            </KunButton>
            <KunButton
              color="danger"
              variant="flat"
              size="sm"
              @click="askDeactivate"
            >
              <KunIcon name="lucide:power-off" class="mr-1 size-4" />
              停用
            </KunButton>
          </div>
        </div>

        <div class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div>
            <p class="text-xs text-default-400">分层</p>
            <p class="mt-0.5 text-sm text-foreground">
              {{ DEV_TIER_LABELS[app.tier] ?? app.tier }}
            </p>
          </div>
          <div>
            <p class="text-xs text-default-400">限流</p>
            <p class="mt-0.5 text-sm text-foreground">
              {{ limitLabel(app.rate_per_min, '次/分') }}
            </p>
          </div>
          <div>
            <p class="text-xs text-default-400">日配额</p>
            <p class="mt-0.5 text-sm text-foreground">
              {{ limitLabel(app.quota_daily, '次/日') }}
            </p>
          </div>
          <div>
            <p class="text-xs text-default-400">创建时间</p>
            <p class="mt-0.5 text-sm text-foreground">
              {{ formatDate(app.created_at, { isShowYear: true }) }}
            </p>
          </div>
        </div>
      </KunCard>

      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold text-foreground">API 密钥</h2>
        <KunButton color="primary" size="sm" @click="showMintModal = true">
          <KunIcon name="lucide:plus" class="mr-1 size-4" />
          生成新密钥
        </KunButton>
      </div>

      <KeysTable
        :keys="keys"
        :busy="busy"
        @rotate="askRotate"
        @revoke="askRevoke"
      />

      <div>
        <h2 class="mb-3 text-lg font-semibold text-foreground">
          用量（最近 7 天）
        </h2>
        <UsageTable :rows="usage" />
      </div>
    </template>

    <AppsEditModal
      v-if="showEditModal && app"
      :app="app"
      @close="showEditModal = false"
      @updated="handleEdited"
    />

    <KeysMintModal
      v-if="showMintModal"
      :client-id="clientId"
      @close="showMintModal = false"
      @minted="handleMinted"
    />

    <KeysRevealModal
      v-if="mintedKey"
      :minted="mintedKey"
      :rotated="revealRotated"
      @close="closeReveal"
    />

    <KunModal
      v-if="confirmDialog"
      :model-value="true"
      role="alertdialog"
      @update:model-value="confirmDialog = null"
    >
      <div class="space-y-4">
        <h2 class="text-xl font-bold text-foreground">
          {{ confirmDialog.title }}
        </h2>
        <p class="text-sm text-default-500">{{ confirmDialog.body }}</p>
        <div class="flex justify-end gap-3">
          <KunButton
            color="default"
            variant="flat"
            :disabled="busy"
            @click="confirmDialog = null"
          >
            取消
          </KunButton>
          <KunButton
            :color="confirmDialog.danger ? 'danger' : 'primary'"
            :disabled="busy"
            @click="runConfirm"
          >
            <KunIcon
              v-if="busy"
              name="lucide:loader-circle"
              class="mr-2 size-4 animate-spin"
            />
            确认
          </KunButton>
        </div>
      </div>
    </KunModal>
  </div>
</template>

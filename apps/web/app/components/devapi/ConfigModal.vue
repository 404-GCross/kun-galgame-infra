<script setup lang="ts">
import {
  DEV_TIER_OPTIONS,
  type DevTier,
  devTierLimitHint,
} from '~/constants/devapi'
import type { DevApp } from '~~/shared/types/devapi'

// Edits an app's dev configuration (tier / nsfw / rate + quota overrides /
// owner). Shared by the list row edit and the detail page. Phase 1 forbids
// granting NSFW (裁定 6) so the nsfw switch is surfaced read-off with a note —
// the field/wire path exists but stays off.
const props = defineProps<{ app: DevApp }>()
const emit = defineEmits<{ close: []; updated: [] }>()

const api = useApi()
const show = ref(true)

const tier = ref<DevTier>((props.app.dev_tier as DevTier) ?? 'free')
const ratePerMin = ref<number | null>(props.app.dev_rate_per_min)
const quotaDaily = ref<number | null>(props.app.dev_quota_daily)
const ownerUserId = ref<number | null>(props.app.owner_user_id ?? null)
const error = ref('')
const isLoading = ref(false)

watch(show, (val) => {
  if (!val) emit('close')
})

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true
  try {
    // Wire body mirrors handler.go patchAppRequest (field-for-field).
    const body: Record<string, unknown> = {
      dev_tier: tier.value,
      dev_rate_per_min: ratePerMin.value ?? 0,
      dev_quota_daily: quotaDaily.value ?? 0,
      owner_user_id: ownerUserId.value ?? 0,
    }
    const res = await api.patch(`/admin/devapi/apps/${props.app.client_id}`, body)
    if (res.code === 0) {
      useKunMessage('配置已更新', 'success')
      emit('updated')
    } else {
      error.value = res.message || '更新失败'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model="show" size="md">
    <div class="space-y-4">
      <h2 class="text-xl font-bold text-foreground">应用配置</h2>

      <div class="rounded-lg bg-default-50 p-3">
        <p class="text-xs text-default-400">{{ app.name }}</p>
        <p class="mt-1 truncate font-mono text-sm text-foreground">{{ app.client_id }}</p>
      </div>

      <KunSelect
        v-model="tier"
        label="Tier（等级）"
        :options="DEV_TIER_OPTIONS"
        :description="`默认限额：${devTierLimitHint(tier)}`"
      />

      <KunNumberInput
        v-model="ratePerMin"
        label="限流覆盖（次/分）"
        :min="0"
        description="0 = 使用 tier 默认值"
      />

      <KunNumberInput
        v-model="quotaDaily"
        label="日配额覆盖（次/日）"
        :min="0"
        description="0 = 使用 tier 默认值"
      />

      <KunNumberInput
        v-model="ownerUserId"
        label="归属用户 ID（owner）"
        :min="0"
        description="第三方开发者应用的所有者用户 ID；0 = 不指定"
      />

      <div class="rounded-lg border border-default-200 p-3">
        <div class="flex items-center gap-2 text-sm text-default-500">
          <KunIcon name="lucide:shield-off" class="size-4 text-default-400" />
          NSFW scope：Phase 1 暂不发放
        </div>
        <p class="mt-1 text-xs text-default-400">
          — 当前所有密钥恒为 SFW 投影；NSFW 授权将在后续阶段开放。
        </p>
      </div>

      <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">
        {{ error }}
      </div>

      <div class="flex justify-end gap-3">
        <KunButton color="default" variant="flat" @click="show = false">
          取消
        </KunButton>
        <KunButton color="primary" :disabled="isLoading" @click="handleSubmit">
          <KunIcon v-if="isLoading" name="lucide:loader-circle" class="mr-2 size-4 animate-spin" />
          保存
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>

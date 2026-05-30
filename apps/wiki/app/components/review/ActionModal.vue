<script setup lang="ts">
// Confirmation + reason input for admin status mutations.
// Calls PUT /admin/galgame/:gid/status with { status, reason? } per
// docs/integration/galgame_wiki/06-admin.md.
import type { REVIEW_ACTIONS } from '~/constants/admin'

type Action = (typeof REVIEW_ACTIONS)[number]

const props = defineProps<{
  open: boolean
  galgameId: number
  action: Action
}>()

const emit = defineEmits<{
  close: []
  done: []
}>()

const api = useApi()
const reason = ref('')
const submitting = ref(false)
const errorMsg = ref('')

watch(
  () => props.open,
  (v) => {
    if (v) {
      reason.value = ''
      errorMsg.value = ''
    }
  }
)

const submit = async () => {
  if (props.action.needs_reason && !reason.value.trim()) {
    errorMsg.value = '请填写理由（必填）'
    return
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    const resp = await api.put<{ id: number; status: number }>(
      `/admin/galgame/${props.galgameId}/status`,
      {
        status: props.action.status,
        ...(reason.value.trim() ? { reason: reason.value.trim() } : {})
      }
    )
    if (resp.code === 0) {
      emit('done')
    } else {
      errorMsg.value = resp.message || '操作失败'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <KunModal
    :model-value="open"
    inner-class-name="max-w-md"
    @update:model-value="(v: boolean) => !v && emit('close')"
  >
    <div class="space-y-4 p-6">
      <div class="flex items-center gap-2">
        <Icon :name="action.icon" class="text-foreground size-5" />
        <h2 class="text-foreground text-lg font-semibold">{{ action.label }}</h2>
      </div>

      <p class="text-default-500 text-sm">
        <template v-if="action.id === 'approve'">
          通过后此条目将变为已发布（status=0），跨站全局可见。
          提交者会收到一条 approved 通知。
        </template>
        <template v-else-if="action.id === 'decline'">
          拒绝后此条目变为已拒绝（status=4），仅提交者可见。
          提交者可修改后重新提交。请填写明确理由。
        </template>
        <template v-else-if="action.id === 'ban'">
          封禁后此条目变为已封禁（status=1），所有人不可见。
          通常用于违规内容，需要慎重操作。
        </template>
      </p>

      <div v-if="action.needs_reason" class="space-y-1">
        <span class="text-foreground text-sm font-medium">
          理由
          <span class="text-danger">*</span>
        </span>
        <KunTextarea
          v-model="reason"
          :rows="4"
          placeholder="向提交者说明理由（会显示在他的通知里）"
        />
      </div>

      <div v-if="!action.needs_reason" class="space-y-1">
        <span class="text-default-500 text-sm">备注（可选）</span>
        <KunTextarea v-model="reason" :rows="2" placeholder="可选审核备注" />
      </div>

      <p v-if="errorMsg" class="text-danger text-sm">{{ errorMsg }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="light" @click="emit('close')">取消</KunButton>
        <KunButton
          :color="(action.color as any)"
          :disabled="submitting"
          @click="submit"
        >
          <Icon
            v-if="submitting"
            name="lucide:loader-2"
            class="mr-1 size-4 animate-spin"
          />
          确认{{ action.label }}
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>

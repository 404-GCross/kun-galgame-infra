<script setup lang="ts">
// Confirmation for one matrix toggle. A permission change is invisible once
// made — the operator sees a tick, not a consequence — so the modal spells out
// the state change and who it lands on before the write goes out.
import type { PermissionKeyRow } from '~~/shared/types/permission'
import { roleLabel } from '~/constants/roles'

const props = defineProps<{
  row: PermissionKeyRow | null
  role: string
  submitting: boolean
}>()
const emit = defineEmits<{ confirm: [] }>()

const open = defineModel<boolean>('open', { required: true })

const granted = computed(() => props.row?.grants[props.role]?.granted ?? false)
const actionLabel = computed(() => (granted.value ? '撤销' : '授予'))
</script>

<template>
  <KunModal v-model="open">
    <div class="space-y-4">
      <h2 class="text-foreground text-xl font-bold">{{ actionLabel }}权限</h2>

      <div v-if="row" class="space-y-2 text-sm">
        <p class="text-default-500">
          将对角色
          <span class="text-foreground font-semibold">
            {{ roleLabel(role) }}（{{ role }}）
          </span>
          {{ actionLabel }}
          <span class="text-foreground font-mono font-semibold break-all">
            {{ row.key }}
          </span>
        </p>
        <p class="text-default-400">{{ row.desc_zh }}</p>
        <p class="text-default-500">
          <template v-if="granted">
            撤销后该角色回到代码捆（地板），不会低于地板；此变更会记入审计。
          </template>
          <template v-else>
            授予后该角色的每一位持有者立即获得此能力；各服务在 30
            秒内生效，此变更会记入审计。
          </template>
        </p>
      </div>

      <div class="flex justify-end gap-3">
        <KunButton color="default" variant="flat" @click="open = false">
          取消
        </KunButton>
        <KunButton
          :color="granted ? 'warning' : 'primary'"
          :loading="submitting"
          @click="emit('confirm')"
        >
          确认{{ actionLabel }}
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>

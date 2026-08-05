<script setup lang="ts">
// Permission matrix console: every live permission key by domain, the five
// contract roles as columns, and — for the cells this caller may change — a
// click that grants or revokes an overlay row.
//
// The page decides NOTHING about authorization. `editable` and `reason` come
// from the server, computed by the same validator the write path runs, so the
// button offered here is exactly the write the API would accept.
import type {
  PermissionMatrix,
  PermissionAuditEntry,
  PermissionKeyRow
} from '~~/shared/types/permission'
import { AUDIT_LIST_LIMIT } from '~/constants/permission'

const api = useApi()

const {
  data: matrixData,
  status,
  refresh: refreshMatrix,
  error
} = await useApiFetch<PermissionMatrix>('/admin/permissions/matrix')

const {
  data: auditData,
  refresh: refreshAudit,
  error: auditError
} = await useApiFetch<PermissionAuditEntry[]>('/admin/permissions/audit', {
  query: { limit: AUDIT_LIST_LIMIT }
})

const matrix = computed(() => matrixData.value)
const auditEntries = computed(() => auditData.value ?? [])
const isLoading = computed(() => status.value === 'pending')

// Pending toggle, held while the confirm modal is open.
const confirmOpen = ref(false)
const pendingRow = ref<PermissionKeyRow | null>(null)
const pendingRole = ref('')
const submitting = ref(false)

const askToggle = (row: PermissionKeyRow, role: string) => {
  if (!row.grants[role]?.editable) return
  pendingRow.value = row
  pendingRole.value = role
  confirmOpen.value = true
}

const confirmToggle = async () => {
  const row = pendingRow.value
  const role = pendingRole.value
  if (!row || !role) return

  const granted = row.grants[role]?.granted ?? false
  submitting.value = true
  try {
    const response = granted
      ? await api.delete('/admin/permissions/overrides', {
          role,
          permission: row.key
        })
      : await api.post('/admin/permissions/overrides', {
          role,
          permission: row.key
        })

    if (response.code === 0) {
      useKunMessage(granted ? '已撤销' : '已授予', 'success')
      confirmOpen.value = false
      await Promise.all([refreshMatrix(), refreshAudit()])
    } else {
      useKunMessage(response.message || '操作失败', 'error')
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-foreground text-2xl font-bold">权限矩阵</h1>
      <p class="text-default-500 mt-1">
        代码捆是地板，叠加层只增不减：点击可编辑的格子即授予或撤销一条叠加授权。
      </p>
    </div>

    <CommonFetchError v-if="error" @retry="refreshMatrix" />

    <div v-else-if="isLoading" class="flex items-center justify-center py-12">
      <KunIcon
        name="lucide:loader-circle"
        class="text-primary size-8 animate-spin"
      />
    </div>

    <template v-else-if="matrix">
      <PermissionLegend
        :manages-permissions="matrix.manages_permissions"
      />

      <PermissionMatrix
        v-for="domain in matrix.domains"
        :key="domain.name"
        :domain="domain"
        :roles="matrix.roles"
        @toggle="askToggle"
      />

      <PermissionAuditList
        :entries="auditEntries"
        :has-error="Boolean(auditError)"
        @retry="refreshAudit"
      />

      <PermissionConfirmModal
        v-model:open="confirmOpen"
        :row="pendingRow"
        :role="pendingRole"
        :submitting="submitting"
        @confirm="confirmToggle"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
// Subject-kind registry: the per-site kinds a report may target, each with an
// optional callback (URL + HMAC secret) the trust service posts dispositions
// to. Rows are registered, their callback config edited, and deprecated — never
// deleted.
import type {
  TrustSubjectKind,
  TrustCreateSubjectKindRequest,
  TrustPatchSubjectKindRequest
} from '~~/shared/types/trust'

const api = useApi('trust')

const site = ref('')
const { data, refresh } = await useApiFetch<TrustSubjectKind[]>(
  '/admin/trust/subject-kinds',
  { query: computed(() => ({ site: site.value || undefined })) },
  'trust'
)
const kinds = computed<TrustSubjectKind[]>(() => data.value ?? [])

// Create
const createOpen = ref(false)
const form = reactive({ site: '', key: '', callback_url: '', callback_secret: '' })
const creating = ref(false)

const openCreate = () => {
  form.site = site.value || ''
  form.key = ''
  form.callback_url = ''
  form.callback_secret = ''
  createOpen.value = true
}

const create = async () => {
  if (!form.site.trim() || !form.key.trim()) {
    useKunMessage('站点与 key 必填', 'warn')
    return
  }
  creating.value = true
  try {
    const body: TrustCreateSubjectKindRequest = {
      site: form.site.trim(),
      key: form.key.trim()
    }
    if (form.callback_url.trim()) body.callback_url = form.callback_url.trim()
    if (form.callback_secret.trim())
      body.callback_secret = form.callback_secret.trim()
    const res = await api.post('/admin/trust/subject-kinds', body)
    if (res.code === 0) {
      useKunMessage('已注册', 'success')
      createOpen.value = false
      await refresh()
    } else {
      useKunMessage(res.message || '注册失败', 'error')
    }
  } finally {
    creating.value = false
  }
}

// Edit callback config
const editOpen = ref(false)
const editTarget = ref<TrustSubjectKind | null>(null)
const editForm = reactive({ callback_url: '', callback_secret: '' })
const saving = ref(false)

const openEdit = (k: TrustSubjectKind) => {
  editTarget.value = k
  editForm.callback_url = k.callback_url ?? ''
  editForm.callback_secret = ''
  editOpen.value = true
}

const patch = async (
  k: TrustSubjectKind,
  body: TrustPatchSubjectKindRequest
) => {
  const res = await api.patch(`/admin/trust/subject-kinds/${k.id}`, body)
  if (res.code === 0) {
    await refresh()
    return true
  }
  useKunMessage(res.message || '更新失败', 'error')
  return false
}

const saveEdit = async () => {
  if (!editTarget.value) return
  saving.value = true
  try {
    const body: TrustPatchSubjectKindRequest = {
      callback_url: editForm.callback_url.trim()
    }
    // A blank secret leaves the stored secret untouched (it is never shown).
    if (editForm.callback_secret.trim())
      body.callback_secret = editForm.callback_secret.trim()
    if (await patch(editTarget.value, body)) {
      useKunMessage('已更新回调配置', 'success')
      editOpen.value = false
    }
  } finally {
    saving.value = false
  }
}

const toggleDeprecated = async (k: TrustSubjectKind) => {
  if (await patch(k, { is_deprecated: !k.is_deprecated })) {
    useKunMessage(k.is_deprecated ? '已启用' : '已弃用', 'success')
  }
}
</script>

<template>
  <KunCard content-class="space-y-3 p-4">
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="text-foreground text-lg font-bold">主体类型</h2>
      <KunInput
        v-model="site"
        placeholder="按站点过滤"
        class="w-40"
      />
      <KunButton color="primary" size="sm" class="ml-auto" @click="openCreate">
        <KunIcon name="lucide:plus" class="mr-1 size-4" />
        新建
      </KunButton>
    </div>

    <div class="overflow-x-auto">
      <table class="w-full min-w-[36rem] text-sm">
        <thead class="text-default-500">
          <tr>
            <th class="px-2 py-2 text-left font-medium">站点</th>
            <th class="px-2 py-2 text-left font-medium">key</th>
            <th class="px-2 py-2 text-left font-medium">回调</th>
            <th class="px-2 py-2 text-left font-medium">状态</th>
            <th class="px-2 py-2 text-right font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="k in kinds"
            :key="k.id"
            class="border-default-200 border-t align-top"
          >
            <td class="px-2 py-2">{{ k.site }}</td>
            <td class="px-2 py-2 font-mono">{{ k.key }}</td>
            <td class="px-2 py-2">
              <div class="flex flex-wrap items-center gap-1">
                <span v-if="k.callback_url" class="text-default-400 max-w-[14rem] truncate">
                  {{ k.callback_url }}
                </span>
                <span v-else class="text-default-300">—</span>
                <KunChip v-if="k.has_secret" color="success" variant="flat" size="xs">
                  密钥
                </KunChip>
              </div>
            </td>
            <td class="px-2 py-2">
              <KunChip
                :color="k.is_deprecated ? 'default' : 'success'"
                variant="flat"
                size="xs"
              >
                {{ k.is_deprecated ? '已弃用' : '启用中' }}
              </KunChip>
            </td>
            <td class="px-2 py-2 text-right">
              <div class="flex justify-end gap-2">
                <KunButton
                  color="default"
                  variant="flat"
                  size="sm"
                  @click="openEdit(k)"
                >
                  编辑
                </KunButton>
                <KunButton
                  :color="k.is_deprecated ? 'success' : 'warning'"
                  variant="flat"
                  size="sm"
                  @click="toggleDeprecated(k)"
                >
                  {{ k.is_deprecated ? '启用' : '弃用' }}
                </KunButton>
              </div>
            </td>
          </tr>
          <tr v-if="!kinds.length">
            <td colspan="5" class="text-default-400 px-2 py-8 text-center">
              暂无主体类型
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <KunModal v-model="createOpen">
      <div class="space-y-4">
        <h2 class="text-foreground text-xl font-bold">注册主体类型</h2>
        <KunInput v-model="form.site" label="站点" placeholder="如 kungal" />
        <KunInput v-model="form.key" label="key" placeholder="如 topic / reply" />
        <KunInput
          v-model="form.callback_url"
          label="回调 URL(可选)"
          placeholder="https://…"
        />
        <KunInput
          v-model="form.callback_secret"
          label="回调密钥(可选)"
          placeholder="HMAC 密钥"
        />
        <div class="flex justify-end gap-3">
          <KunButton color="default" variant="flat" @click="createOpen = false">
            取消
          </KunButton>
          <KunButton color="primary" :loading="creating" @click="create">
            注册
          </KunButton>
        </div>
      </div>
    </KunModal>

    <KunModal v-model="editOpen">
      <div class="space-y-4">
        <h2 class="text-foreground text-xl font-bold">
          编辑回调 · {{ editTarget?.site }}/{{ editTarget?.key }}
        </h2>
        <KunInput
          v-model="editForm.callback_url"
          label="回调 URL"
          placeholder="留空则清除"
        />
        <KunInput
          v-model="editForm.callback_secret"
          label="回调密钥"
          placeholder="留空则保持不变"
        />
        <div class="flex justify-end gap-3">
          <KunButton color="default" variant="flat" @click="editOpen = false">
            取消
          </KunButton>
          <KunButton color="primary" :loading="saving" @click="saveEdit">
            保存
          </KunButton>
        </div>
      </div>
    </KunModal>
  </KunCard>
</template>

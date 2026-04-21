<script setup lang="ts">
import type { GalgameLink } from '~/shared/types/galgame'

const props = defineProps<{ galgameId: number }>()

const api = useApi()
const items = ref<GalgameLink[]>([])
const loading = ref(false)
const form = ref({ name: '', link: '' })
const submitting = ref(false)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<GalgameLink[]>(
      `/galgame/${props.galgameId}/links`
    )
    if (response.code === 0) items.value = response.data ?? []
  } finally {
    loading.value = false
  }
}

const add = async () => {
  if (!form.value.name.trim() || !form.value.link.trim()) return
  submitting.value = true
  try {
    const response = await api.post(`/galgame/${props.galgameId}/links`, {
      name: form.value.name.trim(),
      link: form.value.link.trim()
    })
    if (response.code === 0) {
      form.value = { name: '', link: '' }
      useKunMessage('添加成功', 'success')
      await load()
    } else {
      useKunMessage(response.message || '添加失败', 'error')
    }
  } finally {
    submitting.value = false
  }
}

const remove = async (id: number, name: string) => {
  if (!confirm(`确认删除链接「${name}」？`)) return
  const response = await api.delete(`/galgame/${props.galgameId}/links`, { id })
  if (response.code === 0) {
    useKunMessage('删除成功', 'success')
    await load()
  } else {
    useKunMessage(response.message || '删除失败', 'error')
  }
}

onMounted(load)
</script>

<template>
  <KunCard class="p-6">
    <h3 class="text-foreground mb-3 text-base font-semibold">
      外部链接 ({{ items.length }})
    </h3>

    <div class="mb-3 grid grid-cols-3 gap-2">
      <KunInput
        v-model="form.name"
        placeholder="名称 (如 Steam)"
        :dark-border="false"
      />
      <KunInput
        v-model="form.link"
        type="url"
        placeholder="https://..."
        :dark-border="false"
        @keydown.enter="add"
      />
      <KunButton
        color="primary"
        :disabled="submitting || !form.name.trim() || !form.link.trim()"
        @click="add"
      >
        添加链接
      </KunButton>
    </div>

    <div
      v-if="items.length === 0 && !loading"
      class="text-default-400 text-sm"
    >
      暂无链接
    </div>

    <ul v-else class="divide-default-200 divide-y">
      <li
        v-for="l in items"
        :key="l.id"
        class="flex items-center justify-between gap-2 py-2 text-sm"
      >
        <div class="min-w-0 flex-1">
          <p class="text-foreground font-medium">{{ l.name }}</p>
          <a
            :href="l.link"
            target="_blank"
            class="text-primary block truncate text-xs hover:underline"
          >
            {{ l.link }} ↗
          </a>
        </div>
        <button
          class="text-default-400 hover:bg-danger-50 hover:text-danger rounded p-1"
          title="删除"
          @click="remove(l.id, l.name)"
        >
          <Icon name="lucide:trash-2" class="size-4" />
        </button>
      </li>
    </ul>
  </KunCard>
</template>

<script setup lang="ts">
import type { GalgameAlias } from '~/shared/types/galgame'

const props = defineProps<{ galgameId: number }>()

const api = useApi()
const items = ref<GalgameAlias[]>([])
const loading = ref(false)
const newName = ref('')
const submitting = ref(false)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<GalgameAlias[]>(
      `/galgame/${props.galgameId}/aliases`
    )
    if (response.code === 0) items.value = response.data ?? []
  } finally {
    loading.value = false
  }
}

const add = async () => {
  const name = newName.value.trim()
  if (!name) return
  submitting.value = true
  try {
    const response = await api.post(`/galgame/${props.galgameId}/aliases`, {
      name
    })
    if (response.code === 0) {
      newName.value = ''
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
  if (
    !(await useKunConfirm({
      title: '删除别名',
      content: `确认删除别名「${name}」？`,
      confirmText: '删除',
      danger: true
    }))
  )
    return
  const response = await api.delete(
    `/galgame/${props.galgameId}/aliases`,
    { id }
  )
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
      别名 ({{ items.length }})
    </h3>

    <div class="mb-3 flex gap-2">
      <div class="flex-1">
        <KunInput
          v-model="newName"
          placeholder="新别名..."
          :dark-border="false"
          @keydown.enter="add"
        />
      </div>
      <KunButton color="primary" :disabled="submitting || !newName.trim()" @click="add">
        添加
      </KunButton>
    </div>

    <div
      v-if="items.length === 0 && !loading"
      class="text-default-400 text-sm"
    >
      暂无别名
    </div>

    <div v-else class="flex flex-wrap gap-2">
      <span
        v-for="a in items"
        :key="a.id"
        class="bg-default-100 text-default-600 flex items-center gap-1 rounded px-2 py-1 text-xs"
      >
        {{ a.name }}
        <button
          class="text-default-400 hover:text-danger ml-1"
          title="删除"
          @click="remove(a.id, a.name)"
        >
          <Icon name="lucide:x" class="size-3" />
        </button>
      </span>
    </div>
  </KunCard>
</template>

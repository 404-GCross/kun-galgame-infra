<script setup lang="ts">
defineProps<{ site: Site }>()
const emit = defineEmits<{
  edit: []
  delete: []
}>()

const showMenu = ref(false)
</script>

<template>
  <div class="rounded-xl bg-content1 p-6 shadow-sm transition-shadow hover:shadow-md">
    <div class="mb-4 flex items-start justify-between">
      <div class="flex size-12 items-center justify-center rounded-lg bg-primary-100">
        <Icon name="lucide:globe" class="size-6 text-primary" />
      </div>

      <KunPopover position="bottom-end">
        <template #trigger>
          <button class="rounded p-1 text-default-300 hover:bg-default-100 hover:text-default-500">
            <Icon name="lucide:more-vertical" class="size-5" />
          </button>
        </template>
        <div class="w-32 py-1">
          <button
            class="flex w-full items-center gap-2 px-3 py-2 text-sm text-default-500 hover:bg-default-100 hover:text-foreground"
            @click="emit('edit')"
          >
            <Icon name="lucide:pencil" class="size-4" />
            编辑
          </button>
          <button
            class="flex w-full items-center gap-2 px-3 py-2 text-sm text-danger hover:bg-danger-50"
            @click="emit('delete')"
          >
            <Icon name="lucide:trash-2" class="size-4" />
            删除
          </button>
        </div>
      </KunPopover>
    </div>

    <h3 class="text-lg font-semibold text-foreground">{{ site.name }}</h3>
    <p class="mt-1 text-sm text-primary">{{ site.domain }}</p>
    <p class="mt-2 line-clamp-2 text-sm text-default-500">
      {{ site.description || '暂无描述' }}
    </p>

    <div class="mt-4 border-t border-default-200 pt-4">
      <span class="text-xs text-default-400">
        创建于 {{ new Date(site.created_at).toLocaleDateString() }}
      </span>
    </div>
  </div>
</template>

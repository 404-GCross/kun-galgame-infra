<script setup lang="ts">
defineProps<{ site: Site }>()
const emit = defineEmits<{
  edit: []
  delete: []
}>()

const showMenu = ref(false)
</script>

<template>
  <KunCard is-hoverable content-class="justify-start gap-0" class-name="p-6">
    <div class="mb-4 flex items-start justify-between">
      <div class="flex size-12 items-center justify-center rounded-lg bg-primary-100">
        <KunIcon name="lucide:globe" class="size-6 text-primary" />
      </div>

      <KunPopover position="bottom-end">
        <template #trigger>
          <KunButton
            variant="light"
            size="sm"
            is-icon-only
            aria-label="更多操作"
          >
            <KunIcon name="lucide:ellipsis-vertical" class="size-5" />
          </KunButton>
        </template>
        <div class="w-32 py-1">
          <button
            class="flex w-full items-center gap-2 px-3 py-2 text-sm text-default-500 hover:bg-default-100 hover:text-foreground"
            @click="emit('edit')"
          >
            <KunIcon name="lucide:pencil" class="size-4" />
            编辑
          </button>
          <button
            class="flex w-full items-center gap-2 px-3 py-2 text-sm text-danger hover:bg-danger-50"
            @click="emit('delete')"
          >
            <KunIcon name="lucide:trash-2" class="size-4" />
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
  </KunCard>
</template>

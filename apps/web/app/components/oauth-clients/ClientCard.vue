<script setup lang="ts">
const props = defineProps<{
  client: OAuthClient
  sites: Site[]
}>()
const emit = defineEmits<{ edit: []; delete: [] }>()

const siteName = computed(() => {
  if (!props.client.site_id) return '未关联'
  const site = props.sites.find((s) => s.id === props.client.site_id)
  return site?.name ?? '未知站点'
})
</script>

<template>
  <div class="rounded-xl bg-content1 p-6 shadow-sm">
    <div class="flex items-start justify-between">
      <div class="flex items-center gap-4">
        <div class="flex size-12 items-center justify-center rounded-lg bg-warning-100">
          <Icon name="lucide:key" class="size-6 text-warning" />
        </div>
        <div>
          <h3 class="text-lg font-semibold text-foreground">{{ client.name }}</h3>
          <p class="text-sm text-default-400">{{ siteName }}</p>
        </div>
      </div>
      <div class="flex gap-1">
        <button
          class="rounded p-1 text-default-300 hover:bg-default-100 hover:text-default-500"
          title="编辑客户端"
          @click="emit('edit')"
        >
          <Icon name="lucide:pencil" class="size-5" />
        </button>
        <button
          class="rounded p-1 text-default-300 hover:bg-danger-50 hover:text-danger"
          title="删除客户端"
          @click="emit('delete')"
        >
          <Icon name="lucide:trash-2" class="size-5" />
        </button>
      </div>
    </div>

    <div class="mt-4 space-y-3">
      <div class="flex items-center justify-between rounded-lg bg-default-50 p-3">
        <div class="min-w-0 flex-1">
          <p class="text-xs text-default-400">Client ID</p>
          <p class="truncate font-mono text-sm text-foreground">{{ client.id }}</p>
        </div>
        <KunCopy :text="client.id" />
      </div>

      <div>
        <p class="text-xs text-default-400">回调地址</p>
        <div class="mt-1 space-y-1">
          <p
            v-for="uri in client.redirect_uris"
            :key="uri"
            class="truncate text-sm text-foreground"
          >
            {{ uri }}
          </p>
        </div>
      </div>

      <div v-if="client.grants?.length">
        <p class="text-xs text-default-400">授权类型</p>
        <div class="mt-1 flex flex-wrap gap-1">
          <KunBadge
            v-for="grant in client.grants"
            :key="grant"
            color="primary"
            variant="flat"
            size="sm"
          >
            {{ grant }}
          </KunBadge>
        </div>
      </div>
    </div>
  </div>
</template>

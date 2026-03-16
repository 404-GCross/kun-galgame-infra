<script setup lang="ts">
defineProps<{ client: OAuthClient }>()
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
          <KunBadge :color="client.is_active ? 'success' : 'default'" variant="flat" size="sm">
            {{ client.is_active ? '已启用' : '已禁用' }}
          </KunBadge>
        </div>
      </div>
      <button class="rounded p-1 text-default-300 hover:bg-default-100 hover:text-default-500">
        <Icon name="lucide:more-vertical" class="size-5" />
      </button>
    </div>

    <div class="mt-4 space-y-3">
      <div class="flex items-center justify-between rounded-lg bg-default-50 p-3">
        <div>
          <p class="text-xs text-default-400">客户端 ID</p>
          <p class="font-mono text-sm text-foreground">{{ client.client_id }}</p>
        </div>
        <KunCopy :text="client.client_id" />
      </div>

      <div>
        <p class="text-xs text-default-400">回调地址</p>
        <p class="text-sm text-foreground">{{ client.redirect_uri }}</p>
      </div>

      <div>
        <p class="text-xs text-default-400">权限范围</p>
        <div class="mt-1 flex flex-wrap gap-1">
          <KunBadge v-for="scope in client.scopes" :key="scope" color="primary" variant="flat" size="sm">
            {{ scope }}
          </KunBadge>
        </div>
      </div>
    </div>
  </div>
</template>

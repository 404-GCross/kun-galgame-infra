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
  <KunCard content-class="justify-start gap-0" class-name="p-6">
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
        <KunButton
          variant="light"
          size="sm"
          is-icon-only
          aria-label="编辑客户端"
          @click="emit('edit')"
        >
          <Icon name="lucide:pencil" class="size-5" />
        </KunButton>
        <KunButton
          variant="light"
          color="danger"
          size="sm"
          is-icon-only
          aria-label="删除客户端"
          @click="emit('delete')"
        >
          <Icon name="lucide:trash-2" class="size-5" />
        </KunButton>
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
          <KunChip
            v-for="grant in client.grants"
            :key="grant"
            color="primary"
            variant="flat"
            size="sm"
          >
            {{ grant }}
          </KunChip>
        </div>
      </div>

      <div v-if="client.allowed_scopes?.length">
        <p class="text-xs text-default-400">允许的 scope</p>
        <div class="mt-1 flex flex-wrap gap-1">
          <KunChip
            v-for="scope in client.allowed_scopes"
            :key="scope"
            :color="scope === 'image:upload' ? 'warning' : 'default'"
            variant="flat"
            size="sm"
          >
            {{ scope }}
          </KunChip>
        </div>
      </div>

      <div>
        <p class="text-xs text-default-400">客户端类型</p>
        <div class="mt-1 flex flex-wrap gap-1">
          <KunChip
            :color="client.is_public ? 'info' : 'secondary'"
            variant="flat"
            size="sm"
          >
            {{ client.is_public ? '公共 (SPA / native)' : '机密 (confidential)' }}
          </KunChip>
          <!-- auto_consent flag: warning color because skipping the
               consent screen is security-sensitive. Make it visually
               obvious which clients have this elevated trust. -->
          <KunChip
            v-if="client.auto_consent"
            color="warning"
            variant="flat"
            size="sm"
          >
            自动同意
          </KunChip>
        </div>
      </div>

      <div v-if="client.refresh_token_ttl_seconds">
        <p class="text-xs text-default-400">refresh_token 有效期</p>
        <p class="mt-1 text-sm text-foreground">
          {{ Math.round(client.refresh_token_ttl_seconds / 86400) }} 天
        </p>
      </div>
    </div>
  </KunCard>
</template>

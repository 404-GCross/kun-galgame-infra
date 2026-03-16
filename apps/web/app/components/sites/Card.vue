<script setup lang="ts">
defineProps<{ site: Site }>()
</script>

<template>
  <div class="rounded-xl bg-content1 p-6 shadow-sm transition-shadow hover:shadow-md">
    <div class="mb-4 flex items-start justify-between">
      <div class="flex size-12 items-center justify-center rounded-lg bg-primary-100">
        <Icon name="lucide:globe" class="size-6 text-primary" />
      </div>
      <button class="rounded p-1 text-default-300 hover:bg-default-100 hover:text-default-500">
        <Icon name="lucide:more-vertical" class="size-5" />
      </button>
    </div>

    <h3 class="text-lg font-semibold text-foreground">{{ site.name }}</h3>
    <p class="mt-1 text-sm text-primary">{{ site.domain }}</p>
    <p class="mt-2 line-clamp-2 text-sm text-default-500">{{ site.description }}</p>

    <div class="mt-4 flex items-center justify-between border-t border-default-200 pt-4">
      <span class="text-xs text-default-400">
        创建于 {{ new Date(site.created_at).toLocaleDateString() }}
      </span>
      <NuxtLink :to="`/sites/${site.id}`" class="text-sm font-medium text-primary hover:underline">
        配置
      </NuxtLink>
    </div>
  </div>
</template>

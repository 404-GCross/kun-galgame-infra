<script setup lang="ts">
defineProps<{ site: Site }>()
</script>

<template>
  <div class="rounded-xl bg-white p-6 shadow-sm transition-shadow hover:shadow-md dark:bg-gray-800">
    <div class="mb-4 flex items-start justify-between">
      <div class="flex size-12 items-center justify-center rounded-lg bg-indigo-100 dark:bg-indigo-900">
        <Icon name="lucide:globe" class="size-6 text-indigo-600 dark:text-indigo-400" />
      </div>
      <button class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700">
        <Icon name="lucide:more-vertical" class="size-5" />
      </button>
    </div>

    <h3 class="text-lg font-semibold text-gray-800 dark:text-white">{{ site.name }}</h3>
    <p class="mt-1 text-sm text-indigo-600">{{ site.domain }}</p>
    <p class="mt-2 line-clamp-2 text-sm text-gray-600 dark:text-gray-400">{{ site.description }}</p>

    <div class="mt-4 flex items-center justify-between border-t pt-4 dark:border-gray-700">
      <span class="text-xs text-gray-500">
        创建于 {{ new Date(site.created_at).toLocaleDateString() }}
      </span>
      <NuxtLink :to="`/sites/${site.id}`" class="text-sm font-medium text-indigo-600 hover:underline">
        配置
      </NuxtLink>
    </div>
  </div>
</template>

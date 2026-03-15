<script setup lang="ts">
import { QUICK_ACTIONS } from '~/constants/dashboard'
</script>

<template>
  <div class="rounded-xl bg-white p-6 shadow-sm dark:bg-gray-800">
    <h2 class="mb-4 text-lg font-semibold text-gray-800 dark:text-white">
      快捷操作
    </h2>
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <NuxtLink
        v-for="action in QUICK_ACTIONS"
        :key="action.to"
        :to="action.to"
        class="flex items-center gap-3 rounded-lg border border-gray-200 p-4 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-700"
      >
        <Icon :name="action.icon" :class="['size-5', action.color]" />
        <span class="text-gray-700 dark:text-gray-300">{{ action.label }}</span>
      </NuxtLink>
    </div>
  </div>
</template>

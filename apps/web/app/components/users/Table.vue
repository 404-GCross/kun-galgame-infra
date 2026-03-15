<script setup lang="ts">
defineProps<{ users: User[] }>()
</script>

<template>
  <div class="rounded-xl bg-white shadow-sm dark:bg-gray-800">
    <div v-if="users.length === 0" class="py-12 text-center">
      <Icon name="lucide:users" class="mx-auto mb-4 size-12 text-gray-300" />
      <p class="text-gray-500 dark:text-gray-400">暂无用户</p>
      <p class="mt-1 text-sm text-gray-400">用户数据将在迁移后显示在这里</p>
    </div>

    <table v-else class="w-full">
      <thead class="border-b bg-gray-50 dark:border-gray-700 dark:bg-gray-900">
        <tr>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">用户</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">邮箱</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">萌萌点</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">状态</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">注册时间</th>
          <th class="px-6 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">操作</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
        <tr v-for="user in users" :key="user.uuid" class="hover:bg-gray-50 dark:hover:bg-gray-700">
          <td class="whitespace-nowrap px-6 py-4">
            <div class="flex items-center gap-3">
              <KunAvatarAvatar :src="user.avatar" :alt="user.name" size="sm" />
              <span class="font-medium text-gray-900 dark:text-white">{{ user.name }}</span>
            </div>
          </td>
          <td class="whitespace-nowrap px-6 py-4 text-gray-500 dark:text-gray-400">{{ user.email }}</td>
          <td class="whitespace-nowrap px-6 py-4 text-gray-500 dark:text-gray-400">{{ user.moemoepoint }}</td>
          <td class="whitespace-nowrap px-6 py-4">
            <UsersStatusBadge :status="user.status" />
          </td>
          <td class="whitespace-nowrap px-6 py-4 text-gray-500 dark:text-gray-400">
            {{ new Date(user.created_at).toLocaleDateString() }}
          </td>
          <td class="whitespace-nowrap px-6 py-4 text-right">
            <button class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-600">
              <Icon name="lucide:more-horizontal" class="size-5" />
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

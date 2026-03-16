<script setup lang="ts">
defineProps<{ users: User[] }>()
</script>

<template>
  <div class="rounded-xl bg-content1 shadow-sm">
    <div v-if="users.length === 0" class="py-12 text-center">
      <Icon name="lucide:users" class="mx-auto mb-4 size-12 text-default-200" />
      <p class="text-default-400">暂无用户</p>
      <p class="mt-1 text-sm text-default-300">用户数据将在迁移后显示在这里</p>
    </div>

    <table v-else class="w-full">
      <thead class="border-b border-default-200 bg-default-50">
        <tr>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-default-400">用户</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-default-400">邮箱</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-default-400">萌萌点</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-default-400">状态</th>
          <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-default-400">注册时间</th>
          <th class="px-6 py-3 text-right text-xs font-medium uppercase tracking-wider text-default-400">操作</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-default-200">
        <tr v-for="user in users" :key="user.uuid" class="hover:bg-default-100">
          <td class="whitespace-nowrap px-6 py-4">
            <div class="flex items-center gap-3">
              <KunAvatarAvatar :src="user.avatar" :alt="user.name" size="sm" />
              <span class="font-medium text-foreground">{{ user.name }}</span>
            </div>
          </td>
          <td class="whitespace-nowrap px-6 py-4 text-default-400">{{ user.email }}</td>
          <td class="whitespace-nowrap px-6 py-4 text-default-400">{{ user.moemoepoint }}</td>
          <td class="whitespace-nowrap px-6 py-4">
            <UsersStatusBadge :status="user.status" />
          </td>
          <td class="whitespace-nowrap px-6 py-4 text-default-400">
            {{ new Date(user.created_at).toLocaleDateString() }}
          </td>
          <td class="whitespace-nowrap px-6 py-4 text-right">
            <button class="rounded p-1 text-default-300 hover:bg-default-100 hover:text-default-500">
              <Icon name="lucide:more-horizontal" class="size-5" />
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

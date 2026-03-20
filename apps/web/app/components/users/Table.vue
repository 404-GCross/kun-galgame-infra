<script setup lang="ts">
defineProps<{ users: User[] }>()
</script>

<template>
  <div class="bg-content1 rounded-xl shadow-sm">
    <div v-if="users.length === 0" class="py-12 text-center">
      <Icon name="lucide:users" class="text-default-200 mx-auto mb-4 size-12" />
      <p class="text-default-400">暂无用户</p>
      <p class="text-default-300 mt-1 text-sm">用户数据将在迁移后显示在这里</p>
    </div>

    <table v-else class="w-full">
      <thead class="border-default-200 bg-default-50 border-b">
        <tr>
          <th
            class="text-default-400 px-6 py-3 text-left text-xs font-medium tracking-wider uppercase"
          >
            用户
          </th>
          <th
            class="text-default-400 px-6 py-3 text-left text-xs font-medium tracking-wider uppercase"
          >
            邮箱
          </th>
          <th
            class="text-default-400 px-6 py-3 text-left text-xs font-medium tracking-wider uppercase"
          >
            萌萌点
          </th>
          <th
            class="text-default-400 px-6 py-3 text-left text-xs font-medium tracking-wider uppercase"
          >
            状态
          </th>
          <th
            class="text-default-400 px-6 py-3 text-left text-xs font-medium tracking-wider uppercase"
          >
            注册时间
          </th>
          <th
            class="text-default-400 px-6 py-3 text-right text-xs font-medium tracking-wider uppercase"
          >
            操作
          </th>
        </tr>
      </thead>
      <tbody class="divide-default-200 divide-y">
        <tr v-for="user in users" :key="user.uuid" class="hover:bg-default-100">
          <td class="px-6 py-4 whitespace-nowrap">
            <div class="flex items-center gap-3">
              <KunAvatar
                :user="{ id: 0, name: user.name, avatar: user.avatar }"
                size="sm"
                :is-navigation="false"
              />
              <span class="text-foreground font-medium">{{ user.name }}</span>
            </div>
          </td>
          <td class="text-default-400 px-6 py-4 whitespace-nowrap">
            {{ user.email }}
          </td>
          <td class="text-default-400 px-6 py-4 whitespace-nowrap">
            {{ user.moemoepoint }}
          </td>
          <td class="px-6 py-4 whitespace-nowrap">
            <UsersStatusBadge :status="user.status" />
          </td>
          <td class="text-default-400 px-6 py-4 whitespace-nowrap">
            {{ new Date(user.created_at).toLocaleDateString() }}
          </td>
          <td class="px-6 py-4 text-right whitespace-nowrap">
            <button
              class="text-default-300 hover:bg-default-100 hover:text-default-500 rounded p-1"
            >
              <Icon name="lucide:more-horizontal" class="size-5" />
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

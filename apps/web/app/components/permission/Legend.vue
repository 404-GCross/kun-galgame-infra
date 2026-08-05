<script setup lang="ts">
// Reading key for the matrix. Without it the three cell states are three
// similar ticks, and the operator cannot tell "this is compiled in" from
// "someone granted this last week".
defineProps<{ managesPermissions: boolean }>()
</script>

<template>
  <KunCard content-class="space-y-3 p-4">
    <div class="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
      <span class="inline-flex items-center gap-2">
        <KunIcon name="lucide:check" class="text-success size-4" />
        <span class="text-default-500">代码捆（地板，不可撤销）</span>
      </span>
      <span class="inline-flex items-center gap-2">
        <span class="inline-flex items-center gap-1">
          <KunIcon name="lucide:check" class="text-primary size-4" />
          <span class="bg-primary size-1.5 rounded-full" />
        </span>
        <span class="text-default-500">叠加授权（可撤销）</span>
      </span>
      <span class="inline-flex items-center gap-2">
        <span class="text-default-300">—</span>
        <span class="text-default-500">未授予</span>
      </span>
      <span class="inline-flex items-center gap-2">
        <KunIcon name="lucide:lock" class="text-default-400 size-4" />
        <span class="text-default-500">不可委派（只能改代码）</span>
      </span>
    </div>

    <p v-if="managesPermissions" class="text-default-400 text-sm">
      你持有 oauth.permissions.manage：可编辑 creator / moderator / admin
      三列，但仍受不可委派与包含性（moderator ⊆ admin ⊆ ren）约束。
    </p>
    <p v-else class="text-default-400 text-sm">
      你只能把自己持有的权限授予严格低于自己管理层级的角色；creator 列仅 ren
      可编辑。
    </p>
  </KunCard>
</template>

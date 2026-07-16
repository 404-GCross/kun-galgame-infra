<script setup lang="ts">
import type { DevUsageDayFace } from '~~/shared/types/dev'

// Basic usage figures: (day, face) rows with request / 4xx / 5xx counts, as
// returned by GET /dev/apps/:client_id/usage (ordered day DESC, face ASC). MVP
// numbers only — curves are a later phase.
defineProps<{ rows: DevUsageDayFace[] }>()

const faceLabel: Record<string, string> = {
  galgame: 'Galgame',
  catalog: 'Catalog'
}
</script>

<template>
  <KunCard v-if="rows.length" content-class="p-0" class-name="overflow-hidden">
    <div class="overflow-x-auto">
      <table class="w-full min-w-[32rem] text-sm">
        <thead>
          <tr class="border-b border-default-200 text-left text-default-400">
            <th class="px-4 py-2 font-medium">日期</th>
            <th class="px-4 py-2 font-medium">面</th>
            <th class="px-4 py-2 text-right font-medium">请求数</th>
            <th class="px-4 py-2 text-right font-medium">4xx</th>
            <th class="px-4 py-2 text-right font-medium">5xx</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in rows"
            :key="`${row.day}-${row.face}`"
            class="border-b border-default-100"
            :class="i === rows.length - 1 && 'border-b-0'"
          >
            <td class="px-4 py-2 font-mono text-default-500">{{ row.day }}</td>
            <td class="px-4 py-2">
              <KunChip color="default" variant="flat" size="xs">
                {{ faceLabel[row.face] ?? row.face }}
              </KunChip>
            </td>
            <td class="px-4 py-2 text-right font-medium text-foreground">
              {{ row.count.toLocaleString() }}
            </td>
            <td
              class="px-4 py-2 text-right"
              :class="row.status_4xx > 0 ? 'text-warning' : 'text-default-400'"
            >
              {{ row.status_4xx.toLocaleString() }}
            </td>
            <td
              class="px-4 py-2 text-right"
              :class="row.status_5xx > 0 ? 'text-danger' : 'text-default-400'"
            >
              {{ row.status_5xx.toLocaleString() }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </KunCard>

  <KunCard v-else content-class="p-10">
    <p class="text-center text-default-400">最近 7 天暂无用量记录</p>
  </KunCard>
</template>

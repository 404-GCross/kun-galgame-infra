<script setup lang="ts">
import { IMAGE_REVIEW_STATUS_MAP } from '~/constants/admin'

interface Props {
  items: ImageAdminRow[]
  bytesHuman: (n: number) => string
}

const props = defineProps<Props>()
const _props = props

interface Emits {
  (e: 'review', hash: string, status: string, reason?: string): void
  (e: 'delete', hash: string, force: boolean): void
}

const emit = defineEmits<Emits>()

const statusColor = (s: string) =>
  IMAGE_REVIEW_STATUS_MAP[s]?.color ?? 'default'

const statusLabel = (s: string) => IMAGE_REVIEW_STATUS_MAP[s]?.label ?? s

const shortHash = (h: string) => `${h.slice(0, 8)}…${h.slice(-4)}`

const copy = (s: string) => navigator.clipboard.writeText(s)

const onReject = (hash: string) => {
  const reason = prompt('拒绝原因（可选）', '')
  emit('review', hash, 'rejected', reason || '')
}
</script>

<template>
  <div class="overflow-x-auto rounded-xl bg-content1 shadow-sm">
    <table class="w-full text-sm">
      <thead class="bg-content2 text-default-500">
        <tr>
          <th class="px-3 py-2 text-left font-medium">预览</th>
          <th class="px-3 py-2 text-left font-medium">Hash / 尺寸</th>
          <th class="px-3 py-2 text-left font-medium">审核</th>
          <th class="px-3 py-2 text-left font-medium">上传</th>
          <th class="px-3 py-2 text-right font-medium">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="item in _props.items"
          :key="item.hash"
          class="border-t border-default-200 align-top"
        >
          <td class="px-3 py-2">
            <a :href="item.url" target="_blank" rel="noopener">
              <img
                :src="item.variant_urls['100'] || item.variant_urls['mini'] || item.url"
                :alt="item.hash"
                class="size-16 rounded-md border border-default-200 object-cover"
              />
            </a>
          </td>
          <td class="px-3 py-2">
            <button
              class="font-mono text-xs text-foreground hover:text-primary"
              :title="item.hash"
              @click="copy(item.hash)"
            >
              {{ shortHash(item.hash) }}
            </button>
            <div class="mt-0.5 text-xs text-default-400">
              {{ item.width }} × {{ item.height }} · {{ _props.bytesHuman(item.size_bytes) }}
            </div>
            <div class="mt-0.5 text-xs text-default-400">
              {{ item.mime }}
            </div>
          </td>
          <td class="px-3 py-2">
            <span
              :class="[
                'inline-block rounded-full px-2 py-0.5 text-xs',
                `bg-${statusColor(item.review_status)}-100`,
                `text-${statusColor(item.review_status)}-700`,
              ]"
            >
              {{ statusLabel(item.review_status) }}
            </span>
            <div
              v-if="item.deleted_at"
              class="mt-0.5 text-xs text-danger-500"
            >
              已软删
            </div>
          </td>
          <td class="px-3 py-2 text-xs text-default-500">
            <div v-if="item.first_uploader_client">
              client: {{ item.first_uploader_client }}
            </div>
            <div v-if="item.first_uploader_sub" class="text-default-400">
              sub: {{ item.first_uploader_sub.slice(0, 12) }}…
            </div>
            <div class="mt-0.5 text-default-400">
              {{ new Date(item.created_at).toLocaleString() }}
            </div>
          </td>
          <td class="px-3 py-2 text-right">
            <div class="inline-flex gap-1">
              <KunButton
                v-if="item.review_status !== 'approved'"
                color="success"
                variant="flat"
                size="sm"
                @click="emit('review', item.hash, 'approved')"
              >
                通过
              </KunButton>
              <KunButton
                v-if="item.review_status !== 'rejected'"
                color="danger"
                variant="flat"
                size="sm"
                @click="onReject(item.hash)"
              >
                拒绝
              </KunButton>
              <KunButton
                color="default"
                variant="flat"
                size="sm"
                @click="emit('delete', item.hash, false)"
              >
                软删
              </KunButton>
              <KunButton
                color="danger"
                variant="solid"
                size="sm"
                @click="emit('delete', item.hash, true)"
              >
                硬删
              </KunButton>
            </div>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="!_props.items.length" class="px-3 py-8 text-center text-default-500">
      无数据
    </div>
  </div>
</template>

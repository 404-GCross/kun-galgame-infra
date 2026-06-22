<script setup lang="ts">
import type { OAuthClient } from '~~/shared/types/oauth-client'

const props = defineProps<{ client: OAuthClient }>()
const emit = defineEmits<{ close: []; updated: [] }>()

const api = useApi()
const show = ref(true)
watch(show, (v) => {
  if (!v) emit('close')
})

const s = props.client.storage

const artifactEnabled = ref(s?.artifact_enabled ?? false)
const artifactSiteKey = ref(s?.artifact_site_key ?? '')
const artifactCdnBase = ref(s?.artifact_cdn_base ?? '')
const artifactMime = ref((s?.artifact_allowed_mime ?? []).join(', '))
const artifactMaxFileSize = ref(s?.artifact_max_file_size ?? 0)
const artifactQuotaDaily = ref(s?.artifact_quota_daily ?? 0)
const artifactQuotaBytesDaily = ref(s?.artifact_quota_bytes_daily ?? 0)

const imageEnabled = ref(s?.image_enabled ?? false)
const imageSiteKey = ref(s?.image_site_key ?? '')
const imageCdnBase = ref(s?.image_cdn_base ?? '')
const imagePresets = ref((s?.image_allowed_presets ?? []).join(', '))
const imageMaxFileSize = ref(s?.image_max_file_size ?? 0)
const imageQuotaDaily = ref(s?.image_quota_daily ?? 0)
const imageQuotaBytesDaily = ref(s?.image_quota_bytes_daily ?? 0)

const isLoading = ref(false)
const error = ref('')

const splitList = (v: string) =>
  v
    .split(/[,\n]/)
    .map((x) => x.trim())
    .filter(Boolean)

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true
  try {
    const res = await api.put(`/oauth/clients/${props.client.id}/storage`, {
      artifact_enabled: artifactEnabled.value,
      artifact_site_key: artifactSiteKey.value,
      artifact_cdn_base: artifactCdnBase.value,
      artifact_allowed_mime: splitList(artifactMime.value),
      artifact_max_file_size: Number(artifactMaxFileSize.value) || 0,
      artifact_quota_daily: Number(artifactQuotaDaily.value) || 0,
      artifact_quota_bytes_daily: Number(artifactQuotaBytesDaily.value) || 0,
      image_enabled: imageEnabled.value,
      image_site_key: imageSiteKey.value,
      image_cdn_base: imageCdnBase.value,
      image_allowed_presets: splitList(imagePresets.value),
      image_max_file_size: Number(imageMaxFileSize.value) || 0,
      image_quota_daily: Number(imageQuotaDaily.value) || 0,
      image_quota_bytes_daily: Number(imageQuotaBytesDaily.value) || 0
    })
    if (res.code === 0) {
      emit('updated')
    } else {
      error.value = res.message || '更新失败'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model="show" size="lg">
    <div class="space-y-4">
      <h2 class="text-foreground text-xl font-bold">
        存储配置 — {{ client.name }}
      </h2>

      <!-- Artifact -->
      <div class="border-default-200 space-y-3 rounded-lg border p-3">
        <KunCheckBox
          v-model="artifactEnabled"
          label="启用 Artifact 上传"
          color="primary"
        />
        <template v-if="artifactEnabled">
          <KunInput
            v-model="artifactSiteKey"
            label="site_key"
            placeholder="如 moyu / kungal"
          />
          <KunInput
            v-model="artifactCdnBase"
            label="CDN 下载域 (cdn_base)"
            placeholder="https://dl.imoe.uk"
          />
          <KunInput
            v-model="artifactMime"
            label="允许的扩展名 / MIME（逗号分隔）"
            placeholder=".zip, .rar, .7z"
          />
          <div>
            <KunInput
              v-model="artifactMaxFileSize"
              type="number"
              label="单文件上限 (bytes)"
            />
            <p class="text-default-400 mt-1 text-xs">
              = {{ formatFileSize(Number(artifactMaxFileSize) || 0) }}
            </p>
          </div>
          <KunInput
            v-model="artifactQuotaDaily"
            type="number"
            label="每日文件数配额"
          />
          <div>
            <KunInput
              v-model="artifactQuotaBytesDaily"
              type="number"
              label="每日字节配额 (bytes)"
            />
            <p class="text-default-400 mt-1 text-xs">
              = {{ formatFileSize(Number(artifactQuotaBytesDaily) || 0) }}
            </p>
          </div>
        </template>
      </div>

      <!-- Image -->
      <div class="border-default-200 space-y-3 rounded-lg border p-3">
        <KunCheckBox
          v-model="imageEnabled"
          label="启用 Image 上传"
          color="primary"
        />
        <template v-if="imageEnabled">
          <KunInput
            v-model="imageSiteKey"
            label="site_key"
            placeholder="如 kungal / galgame_wiki"
          />
          <KunInput
            v-model="imageCdnBase"
            label="CDN (cdn_base)"
            placeholder="https://image.kungal.iloveren.link"
          />
          <KunInput
            v-model="imagePresets"
            label="允许的 presets（逗号分隔）"
            placeholder="avatar, banner"
          />
          <div>
            <KunInput
              v-model="imageMaxFileSize"
              type="number"
              label="单文件上限 (bytes)"
            />
            <p class="text-default-400 mt-1 text-xs">
              = {{ formatFileSize(Number(imageMaxFileSize) || 0) }}
            </p>
          </div>
          <KunInput
            v-model="imageQuotaDaily"
            type="number"
            label="每日文件数配额"
          />
          <div>
            <KunInput
              v-model="imageQuotaBytesDaily"
              type="number"
              label="每日字节配额 (bytes)"
            />
            <p class="text-default-400 mt-1 text-xs">
              = {{ formatFileSize(Number(imageQuotaBytesDaily) || 0) }}
            </p>
          </div>
        </template>
      </div>

      <p v-if="error" class="text-danger text-sm">{{ error }}</p>

      <div class="flex justify-end gap-3">
        <KunButton color="default" variant="flat" @click="show = false">
          取消
        </KunButton>
        <KunButton color="primary" :disabled="isLoading" @click="handleSubmit">
          <KunIcon
            v-if="isLoading"
            name="lucide:loader-circle"
            class="mr-2 size-4 animate-spin"
          />
          保存
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>

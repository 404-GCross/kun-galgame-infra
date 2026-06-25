// Uploads an OAuth-client logo/icon to image_service and returns its CDN URL.
//
// `POST {apiBase}/oauth/clients/logo` (admin, multipart field `file`) is
// decoupled from any client id — it just stores the image and hands back the
// absolute `data.url`, which the create/edit form then saves into `logo_url`.
// Works for both create (no id yet) and edit.
//
// Mirrors the auth/fetch pattern in `components/users/AvatarUploadModal.vue`
// (Bearer access_token cookie + credentials:include + FormData).

interface LogoUploadResponse {
  code: number
  message: string
  data?: { url: string }
}

export const useClientLogoUpload = () => {
  const uploading = ref(false)
  const error = ref('')

  const upload = async (blob: Blob): Promise<string | null> => {
    if (uploading.value) return null
    uploading.value = true
    error.value = ''

    try {
      const fd = new FormData()
      // Blob has no filename; the multipart parser needs one to treat this
      // part as a file rather than a plain form field.
      fd.append('file', blob, 'logo.webp')

      const cfg = useRuntimeConfig()
      const cookie = useCookie('access_token')
      const res = await $fetch<LogoUploadResponse>(
        `${cfg.public.apiBase}/oauth/clients/logo`,
        {
          method: 'POST',
          body: fd,
          headers: cookie.value ? { Authorization: `Bearer ${cookie.value}` } : {},
          credentials: 'include',
        }
      )

      if (res.code === 0 && res.data?.url) {
        return res.data.url
      }
      error.value = res.message || '上传失败'
      return null
    } catch (err) {
      const e = err as { data?: { message?: string }; statusMessage?: string; message?: string }
      error.value = e?.data?.message || e?.statusMessage || e?.message || '网络错误'
      return null
    } finally {
      uploading.value = false
    }
  }

  return { uploading, error, upload }
}

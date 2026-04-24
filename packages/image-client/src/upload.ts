// Upload helpers. Most calling sites will proxy uploads through their own
// backend (which uses the Go SDK), but sites that want frontend direct
// upload can use these helpers.
//
// Frontend direct upload requires the image service's CORS to be enabled
// and the user to have a JWT with image:upload scope. See docs/image_service.

export interface UploadResult {
  hash: string
  url: string
  variant_urls: Record<string, string>
  width: number
  height: number
  size_bytes: number
  deduplicated: boolean
}

export interface UploadOptions {
  /** Base URL of the image service, e.g. https://image.api.example.com */
  baseUrl: string
  /** Bearer token (user JWT for direct upload, or an opaque service token) */
  token: string
  /** OAuth client id this upload is scoped to. Image service uses this to
   *  resolve the site's presets / quota. */
  clientId: string
  /** AbortSignal for request cancellation */
  signal?: AbortSignal
}

export class ImageServiceError extends Error {
  code: number
  status: number
  details?: unknown
  constructor(status: number, code: number, message: string, details?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

/**
 * Upload a File or Blob to the image service via multipart/form-data.
 * Caller-side validation (file size, type) is the caller's responsibility
 * — the image service enforces its own limits and returns 4xx on violation.
 */
export const uploadImage = async (
  file: File | Blob,
  presetName: string,
  opts: UploadOptions
): Promise<UploadResult> => {
  const fd = new FormData()
  fd.append('file', file)
  fd.append('preset', presetName)

  const res = await fetch(`${opts.baseUrl.replace(/\/+$/, '')}/image/upload`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${opts.token}`,
      'X-Kun-Image-Client-Id': opts.clientId
    },
    body: fd,
    signal: opts.signal
  })

  const body = await res.json().catch(() => ({}))

  if (!res.ok) {
    throw new ImageServiceError(
      res.status,
      body.code ?? 0,
      body.message ?? `HTTP ${res.status}`,
      body.details
    )
  }

  return body.data as UploadResult
}

export interface ReferencePingResult {
  updated: number
  not_found: string[]
}

/**
 * Refresh last_referenced_at for the given hashes.
 *
 * Caller-side cron usage: collect all non-null *_image_hash fields from
 * local DB and batch them (max 1000 per call).
 */
export const referencePing = async (
  hashes: string[],
  opts: UploadOptions
): Promise<ReferencePingResult> => {
  if (hashes.length === 0) return { updated: 0, not_found: [] }
  if (hashes.length > 1000)
    throw new Error('referencePing: max 1000 hashes per call')

  const res = await fetch(
    `${opts.baseUrl.replace(/\/+$/, '')}/image/reference-ping`,
    {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${opts.token}`,
        'X-Kun-Image-Client-Id': opts.clientId,
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ hashes }),
      signal: opts.signal
    }
  )

  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ImageServiceError(
      res.status,
      body.code ?? 0,
      body.message ?? `HTTP ${res.status}`
    )
  }
  return body.data as ReferencePingResult
}

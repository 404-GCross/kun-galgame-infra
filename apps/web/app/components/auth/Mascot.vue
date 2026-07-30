<script setup lang="ts">
// Bottom-of-panel decoration for AuthShell's brand panel.
//
// The artwork lives at `public/mascot/auth.webp` and is deliberately NOT
// committed — it is generated art that gets dropped in later. So the image is
// optional: a native <img> is used (KunImage/KunImageNative expose no `error`
// event, only a static fallbackSrc) and on load failure it hides itself.
// The flat solid discs behind it own the height, so the panel looks finished
// and never jumps whether or not the file exists.
//
// <ClientOnly> is what makes that self-hiding reliable: an SSR-rendered <img>
// can finish (and fail) its request before hydration attaches the listener, so
// the error would be missed and the dead node would linger.
// Bound (not a literal `src=`): a literal would make Vite try to resolve the
// asset at build time and fail while the file is absent — the whole point is
// that it may not be there. As a runtime path it is served from public/.
const MASCOT_SRC = '/mascot/auth.webp'

const hasArt = ref(true)

const hideArt = () => {
  hasArt.value = false
}
</script>

<template>
  <div class="relative flex h-52 items-end justify-center">
    <span
      class="bg-primary-100 absolute bottom-0 left-1/2 h-12 w-56 -translate-x-1/2 rounded-full"
      aria-hidden="true"
    />
    <span
      class="border-primary-200 absolute bottom-10 left-1/2 size-32 -translate-x-1/2 rounded-full border-2"
      aria-hidden="true"
    />
    <span
      class="bg-primary-200 absolute bottom-20 left-2 size-10 rounded-full"
      aria-hidden="true"
    />
    <span
      class="bg-primary-200 absolute right-4 bottom-8 size-5 rounded-full"
      aria-hidden="true"
    />

    <ClientOnly>
      <img
        v-if="hasArt"
        :src="MASCOT_SRC"
        alt=""
        class="relative max-h-52 w-auto object-contain"
        @error="hideArt"
      >
    </ClientOnly>
  </div>
</template>

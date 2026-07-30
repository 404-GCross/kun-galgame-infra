<script setup lang="ts">
// Shared two-panel shell for the auth pages (login / register / forgot /
// reset / logout). Purely presentational: it owns the card, the brand panel
// and the page width, and renders the page's form through the default slot —
// no auth state, no routing, no props that could drift from behaviour.
//
// Desktop (lg+): brand panel | form panel, side by side inside one card.
// Mobile: the brand panel degrades to a compact header row above the form
// (mascot dropped — vertical space is the scarce resource there).
</script>

<template>
  <KunCard
    padding="none"
    class-name="w-full max-w-4xl overflow-hidden"
    content-class="grid grid-cols-1 justify-stretch gap-0 lg:grid-cols-[5fr_6fr]"
  >
    <div
      class="bg-primary-50 flex flex-col justify-between gap-8 p-6 lg:p-10"
    >
      <div class="flex items-center gap-3 lg:flex-col lg:items-start lg:gap-4">
        <KunImage
          src="/favicon.webp"
          alt="鲲 Galgame"
          :width="48"
          :height="48"
          object-fit="cover"
          class-name="size-10 shrink-0 overflow-hidden rounded-xl lg:size-12"
        />
        <div class="min-w-0">
          <p class="text-foreground text-lg font-bold lg:text-xl">
            鲲 Galgame 账号
          </p>
          <p class="text-default-500 mt-0.5 text-sm">
            一个账号，登录全部 ACG 站点
          </p>
        </div>
      </div>

      <!-- Mobile drops the mascot: the brand panel is just a header row there -->
      <div class="hidden lg:block">
        <AuthMascot />
      </div>
    </div>

    <div class="p-6 sm:p-8 lg:p-10">
      <slot />
    </div>
  </KunCard>
</template>

<script setup lang="ts">
// Documentation shell for the whole /docs/** section. The sidebar lives HERE,
// in the layout, so it stays mounted across in-section navigation (face → face,
// operation → operation) — only the content column, held by <NuxtPage>, runs
// the page transition. Two columns on lg+, stacked (sidebar disclosure on top)
// below. Single real root element (CLAUDE.md §11).
</script>

<template>
  <div class="flex min-h-screen flex-col bg-background">
    <LayoutHeader />
    <div
      class="mx-auto flex w-full max-w-7xl flex-1 flex-col px-4 md:px-6 lg:flex-row lg:gap-8"
    >
      <DocsSidebar />
      <main class="min-w-0 flex-1 py-8 lg:py-10">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
const route = useRoute()
const { findFace, faceOperationCount } = useDocs()

const face = computed(() => findFace(route.params.face as string))

if (!face.value) {
  throw createError({ statusCode: 404, statusMessage: '未找到该数据面', fatal: true })
}

const current = computed(() => face.value!)

useSeoMeta({
  title: () => `${current.value.label} 面 · API 文档`,
  description: () =>
    `${current.value.label} 面的公开端点目录，共 ${faceOperationCount(current.value)} 个。`
})
</script>

<template>
  <div class="space-y-8">
    <nav class="flex items-center gap-1.5 text-sm text-default-400">
      <NuxtLink to="/docs" class="transition-colors hover:text-foreground">
        文档
      </NuxtLink>
      <KunIcon name="lucide:chevron-right" class="size-3.5" />
      <span class="text-default-500">{{ current.label }} 面</span>
    </nav>

    <header>
      <h1 class="text-2xl font-bold tracking-tight text-foreground">
        {{ current.label }} 面
      </h1>
      <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
        <span class="text-default-500">
          {{ faceOperationCount(current) }} 个公开端点
        </span>
        <code class="font-mono text-xs text-default-400">
          {{ current.baseUrl }}/v1/{{ current.key }}
        </code>
      </div>
    </header>

    <section
      v-for="group in current.groups"
      :key="group.tag"
      class="space-y-3"
    >
      <h2
        v-if="current.groups.length > 1"
        class="text-xs font-semibold uppercase tracking-wider text-default-400"
      >
        {{ group.title }}
      </h2>
      <ul class="divide-y divide-default-100 overflow-hidden rounded-xl border border-default-200">
        <li v-for="op in group.operations" :key="op.id">
          <NuxtLink
            :to="`/docs/${current.key}/${op.id}`"
            class="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-default-50"
          >
            <DocsMethodBadge :method="op.method" size="md" />
            <div class="min-w-0 flex-1">
              <code class="font-mono text-sm text-foreground">{{ op.path }}</code>
              <p class="mt-0.5 text-sm leading-relaxed text-default-500">
                {{ op.summary }}
              </p>
            </div>
            <KunIcon
              name="lucide:chevron-right"
              class="mt-1 size-4 shrink-0 text-default-300"
            />
          </NuxtLink>
        </li>
      </ul>
    </section>
  </div>
</template>

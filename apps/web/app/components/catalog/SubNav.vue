<script setup lang="ts">
import { CANDIDATE_STATUS, PROPOSAL_STATUS } from '~/constants/catalog'
import type {
  CatalogCandidatePage,
  CatalogProposalPage,
  CatalogProbableRefPage
} from '~~/shared/types/catalog'

const route = useRoute()

const { data: pendingCandidates } = await useApiFetch<CatalogCandidatePage>(
  '/admin/catalog/candidates',
  { query: { status: CANDIDATE_STATUS.pending, limit: 1 } },
  'catalog'
)
const { data: openProposals } = await useApiFetch<CatalogProposalPage>(
  '/admin/catalog/proposals',
  { query: { status: PROPOSAL_STATUS.open, limit: 1 } },
  'catalog'
)
const { data: probableRefs } = await useApiFetch<CatalogProbableRefPage>(
  '/admin/catalog/refs/probable',
  { query: { limit: 1 } },
  'catalog'
)

const tabs = computed(() => [
  {
    to: '/catalog/candidates',
    label: '候选',
    icon: 'lucide:git-compare',
    count: pendingCandidates.value?.total ?? 0
  },
  {
    to: '/catalog/proposals',
    label: '合并提案',
    icon: 'lucide:git-merge',
    count: openProposals.value?.total ?? 0
  },
  {
    to: '/catalog/refs',
    label: '外链确认',
    icon: 'lucide:link',
    count: probableRefs.value?.total ?? 0
  }
])
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <KunButton
      v-for="tab in tabs"
      :key="tab.to"
      :color="route.path === tab.to ? 'primary' : 'default'"
      :variant="route.path === tab.to ? 'solid' : 'flat'"
      size="sm"
      @click="navigateTo(tab.to)"
    >
      <KunIcon :name="tab.icon" class="mr-1 size-4" />
      {{ tab.label }}
      <KunChip
        v-if="tab.count > 0"
        :color="route.path === tab.to ? 'default' : 'warning'"
        variant="flat"
        size="xs"
        class="ml-2"
      >
        {{ tab.count }}
      </KunChip>
    </KunButton>
  </div>
</template>

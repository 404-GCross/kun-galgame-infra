<script setup lang="ts">
definePageMeta({
  middleware: ['auth', 'admin'],
})

interface Game {
  id: number
  uuid: string
  title: string
  description: string
  cover_image: string
  status: string
  created_at: string
}

const api = useApi()

const games = ref<Game[]>([])
const isLoading = ref(true)
const searchQuery = ref('')

const filteredGames = computed(() => {
  if (!searchQuery.value) return games.value
  const query = searchQuery.value.toLowerCase()
  return games.value.filter(game =>
    game.title.toLowerCase().includes(query)
  )
})

const fetchGames = async () => {
  isLoading.value = true
  try {
    const response = await api.get<Game[]>('/games')
    if (response.code === 0) {
      games.value = response.data
    }
  } catch (error) {
    console.error('Failed to fetch games:', error)
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  fetchGames()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
          Games
        </h1>
        <p class="mt-1 text-gray-600 dark:text-gray-400">
          Manage visual novel game entries
        </p>
      </div>
    </div>

    <!-- Search -->
    <div class="rounded-xl bg-white p-4 shadow-sm dark:bg-gray-800">
      <div class="relative">
        <Icon
          name="lucide:search"
          class="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-gray-400"
        />
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Search games..."
          class="w-full rounded-lg border border-gray-200 py-2 pl-10 pr-4 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-gray-700 dark:bg-gray-900"
        />
      </div>
    </div>

    <!-- Games Grid -->
    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
    </div>

    <div v-else-if="filteredGames.length === 0" class="rounded-xl bg-white py-12 text-center shadow-sm dark:bg-gray-800">
      <Icon name="lucide:gamepad-2" class="mx-auto mb-4 size-12 text-gray-300" />
      <p class="text-gray-500 dark:text-gray-400">No games found</p>
    </div>

    <div v-else class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      <div
        v-for="game in filteredGames"
        :key="game.id"
        class="group overflow-hidden rounded-xl bg-white shadow-sm transition-shadow hover:shadow-md dark:bg-gray-800"
      >
        <div class="aspect-[16/9] bg-gray-200 dark:bg-gray-700">
          <img
            v-if="game.cover_image"
            :src="game.cover_image"
            :alt="game.title"
            class="size-full object-cover"
          />
          <div v-else class="flex size-full items-center justify-center">
            <Icon name="lucide:image" class="size-12 text-gray-400" />
          </div>
        </div>

        <div class="p-4">
          <h3 class="line-clamp-1 font-semibold text-gray-800 dark:text-white">
            {{ game.title }}
          </h3>
          <p class="mt-1 line-clamp-2 text-sm text-gray-600 dark:text-gray-400">
            {{ game.description }}
          </p>
          <div class="mt-3 flex items-center justify-between">
            <span class="text-xs text-gray-500">
              {{ new Date(game.created_at).toLocaleDateString() }}
            </span>
            <NuxtLink
              :to="`/games/${game.id}`"
              class="text-sm font-medium text-indigo-600 opacity-0 transition-opacity group-hover:opacity-100"
            >
              View
            </NuxtLink>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

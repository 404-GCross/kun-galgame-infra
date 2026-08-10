import type { User } from '~~/shared/types/dev'

export const useUserStore = defineStore(
  'user',
  () => {
    const user = ref<User | null>(null)

    const isLoggedIn = computed(() => !!user.value)

    const setUser = (u: User) => {
      user.value = u
    }

    const clearUser = () => {
      user.value = null
    }

    return { user, isLoggedIn, setUser, clearUser }
  },
  {
    persist: {
      pick: ['user']
    }
  }
)

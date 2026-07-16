import type { User } from '~~/shared/types/dev'

// The signed-in ecosystem account. Persisted (cookie) so a full reload keeps the
// user while the client plugin re-validates the session.
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

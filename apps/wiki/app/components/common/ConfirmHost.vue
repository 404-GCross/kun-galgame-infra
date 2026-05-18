<script setup lang="ts">
// Single global host for useKunConfirm(). Mounted once in app.vue next
// to <KunAlertMessageContainer />. KunModal teleports to body and renders
// nothing while state.open is false (incl. during SSR).
const state = useKunConfirmState()
</script>

<template>
  <KunModal
    :modal-value="state.open"
    inner-class-name="max-w-md"
    @update:modal-value="(v: boolean) => !v && resolveKunConfirm(false)"
  >
    <div class="space-y-4 p-6">
      <h2 class="text-foreground text-lg font-semibold">{{ state.title }}</h2>
      <p class="text-default-600 text-sm whitespace-pre-line">
        {{ state.content }}
      </p>
      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="light" @click="resolveKunConfirm(false)">
          {{ state.cancelText }}
        </KunButton>
        <KunButton
          :color="state.danger ? 'danger' : 'primary'"
          @click="resolveKunConfirm(true)"
        >
          {{ state.confirmText }}
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>

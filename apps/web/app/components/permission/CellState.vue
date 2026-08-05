<script setup lang="ts">
// The glyph for one matrix square. It exists as its own component because the
// same four states have to render identically inside a clickable cell and
// inside an inert one — and a denied cell that looked like an ungranted one
// would hide the single most consequential thing the overlay can do.
import type { PermissionCell } from '~~/shared/types/permission'

defineProps<{ cell: PermissionCell }>()
</script>

<template>
  <span class="inline-flex items-center gap-1">
    <!-- Denied: the role would hold this key, and a deny row takes it away. -->
    <KunChip
      v-if="cell.source === 'deny'"
      color="danger"
      variant="flat"
      size="xs"
    >
      <span class="line-through">已撤销</span>
    </KunChip>

    <template v-else-if="cell.granted">
      <KunIcon
        name="lucide:check"
        class="size-4"
        :class="cell.source === 'grant' ? 'text-primary' : 'text-success'"
      />
      <!-- The dot marks the grant as an overlay row rather than the floor. -->
      <span
        v-if="cell.source === 'grant'"
        class="bg-primary size-1.5 rounded-full"
      />
    </template>

    <span v-else class="text-default-300">—</span>
  </span>
</template>

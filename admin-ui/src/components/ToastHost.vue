<script setup lang="ts">
import { useToastStore } from '../stores/toast'

const toasts = useToastStore()

const styles: Record<string, string> = {
  success: 'border-emerald-700 bg-emerald-950 text-emerald-200',
  error: 'border-red-700 bg-red-950 text-red-200',
  info: 'border-sky-700 bg-sky-950 text-sky-200',
}
</script>

<template>
  <div class="pointer-events-none fixed bottom-4 right-4 z-50 flex w-96 flex-col gap-2">
    <div
      v-for="t in toasts.toasts"
      :key="t.id"
      class="pointer-events-auto flex items-start justify-between gap-3 rounded-lg border px-4 py-3 text-sm shadow-lg"
      :class="styles[t.kind]"
    >
      <span class="break-words">{{ t.message }}</span>
      <button class="shrink-0 opacity-60 hover:opacity-100" @click="toasts.dismiss(t.id)">✕</button>
    </div>
  </div>
</template>

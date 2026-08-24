<script setup lang="ts">
defineProps<{ title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <div class="fixed inset-0 z-40 flex items-center justify-center p-4" @keydown.esc="emit('close')">
    <div class="absolute inset-0 bg-black/60" @click="emit('close')" />
    <div
      class="relative z-10 w-full rounded-xl border border-slate-700 bg-slate-900 shadow-2xl"
      :class="wide ? 'max-w-3xl' : 'max-w-lg'"
    >
      <div class="flex items-center justify-between border-b border-slate-800 px-5 py-3">
        <h2 class="text-sm font-semibold text-slate-100">{{ title }}</h2>
        <button class="text-slate-400 hover:text-slate-200" @click="emit('close')" aria-label="Close">✕</button>
      </div>
      <div class="max-h-[75vh] overflow-y-auto px-5 py-4">
        <slot />
      </div>
      <div v-if="$slots.footer" class="flex justify-end gap-2 border-t border-slate-800 px-5 py-3">
        <slot name="footer" />
      </div>
    </div>
  </div>
</template>

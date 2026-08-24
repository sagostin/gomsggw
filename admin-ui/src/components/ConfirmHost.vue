<script setup lang="ts">
import { useConfirmStore } from '../stores/confirm'

const confirm = useConfirmStore()
</script>

<template>
  <div v-if="confirm.current" class="fixed inset-0 z-50 flex items-center justify-center p-4">
    <div class="absolute inset-0 bg-black/60" @click="confirm.answer(false)" />
    <div class="relative z-10 w-full max-w-md rounded-xl border border-slate-700 bg-slate-900 shadow-2xl">
      <div class="border-b border-slate-800 px-5 py-3">
        <h2 class="text-sm font-semibold text-slate-100">{{ confirm.current.title }}</h2>
      </div>
      <div class="px-5 py-4 text-sm text-slate-300">
        {{ confirm.current.message }}
      </div>
      <div class="flex justify-end gap-2 border-t border-slate-800 px-5 py-3">
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="confirm.answer(false)"
        >
          Cancel
        </button>
        <button
          class="rounded-md px-3 py-1.5 text-sm font-medium text-white"
          :class="confirm.current.danger ? 'bg-red-600 hover:bg-red-500' : 'bg-indigo-600 hover:bg-indigo-500'"
          @click="confirm.answer(true)"
        >
          {{ confirm.current.confirmLabel || 'Confirm' }}
        </button>
      </div>
    </div>
  </div>
</template>

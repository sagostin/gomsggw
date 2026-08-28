<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getLegacyStatus } from '../../api'
import type { Client, LegacyStatus } from '../../api/types'
import { useToastStore } from '../../stores/toast'
import { errorMessage, formatDate } from '../../utils/format'

const props = defineProps<{ client: Client }>()

const toasts = useToastStore()
const status = ref<LegacyStatus | null>(null)
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    status.value = await getLegacyStatus(props.client.id)
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="max-w-2xl">
    <div class="mb-4 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-slate-100">Legacy status (SMPP / MM4)</h2>
      <button
        class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
        @click="load"
      >
        Refresh
      </button>
    </div>

    <div v-if="loading" class="text-sm text-slate-500">Loading…</div>
    <div v-else-if="status" class="space-y-4">
      <div class="grid gap-4 sm:grid-cols-2">
        <!-- SMPP -->
        <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
          <div class="mb-2 flex items-center justify-between">
            <span class="rounded-full bg-emerald-900/40 px-2 py-0.5 text-xs font-medium text-emerald-300">SMPP</span>
            <span class="text-lg leading-none" :class="status.online ? 'text-emerald-400' : 'text-red-400'">●</span>
          </div>
          <div class="text-sm font-medium" :class="status.online ? 'text-emerald-300' : 'text-red-300'">
            {{ status.online ? 'ONLINE' : 'OFFLINE' }}
          </div>
          <div v-if="status.ip" class="mt-1 text-xs text-slate-500">{{ status.ip }}</div>
          <div v-else-if="!status.online" class="mt-1 text-xs text-slate-600">No active bind</div>
        </div>

        <!-- MM4 -->
        <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
          <div class="mb-2 flex items-center justify-between">
            <span class="rounded-full bg-sky-900/40 px-2 py-0.5 text-xs font-medium text-sky-300">MM4</span>
            <span class="text-lg leading-none" :class="status.mm4?.online ? 'text-emerald-400' : 'text-red-400'">●</span>
          </div>
          <div class="text-sm font-medium" :class="status.mm4?.online ? 'text-emerald-300' : 'text-red-300'">
            {{ status.mm4?.online ? 'ONLINE' : 'OFFLINE' }}
          </div>
          <div v-if="status.mm4?.online" class="mt-1 text-xs text-slate-500">
            {{ status.mm4.active_sessions }} session(s) · active {{ formatDate(status.mm4.last_activity_at) }}
          </div>
          <div v-else class="mt-1 text-xs text-slate-600">No active sessions</div>
        </div>
      </div>

      <div class="rounded-lg border border-slate-800 bg-slate-900">
        <h3 class="border-b border-slate-800 px-4 py-3 text-sm font-semibold text-slate-100">
          Failovers ({{ status.failovers?.length ?? 0 }})
        </h3>
        <div v-if="!status.failovers?.length" class="px-4 py-6 text-sm text-slate-500">No failovers configured.</div>
        <div v-else>
          <div
            v-for="f in status.failovers"
            :key="f.username"
            class="flex items-center justify-between border-b border-slate-800/50 px-4 py-2 text-sm"
          >
            <div>
              <span :class="f.online ? 'text-emerald-400' : 'text-red-400'">●</span>
              <span class="ml-2 text-slate-200">{{ f.username }}</span>
              <span class="ml-1 text-slate-500">({{ f.name || 'no name' }})</span>
            </div>
            <span class="text-xs text-slate-500">priority {{ f.priority }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

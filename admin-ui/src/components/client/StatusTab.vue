<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getSmppStatus } from '../../api'
import type { Client, SmppStatus } from '../../api/types'
import { useToastStore } from '../../stores/toast'
import { errorMessage } from '../../utils/format'

const props = defineProps<{ client: Client }>()

const toasts = useToastStore()
const status = ref<SmppStatus | null>(null)
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    status.value = await getSmppStatus(props.client.id)
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
      <h2 class="text-sm font-semibold text-slate-100">SMPP session status</h2>
      <button
        class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
        @click="load"
      >
        Refresh
      </button>
    </div>

    <div v-if="loading" class="text-sm text-slate-500">Loading…</div>
    <div v-else-if="status" class="space-y-4">
      <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
        <div class="flex items-center gap-3">
          <span class="text-lg" :class="status.online ? 'text-emerald-400' : 'text-red-400'">●</span>
          <div>
            <div class="text-sm font-medium" :class="status.online ? 'text-emerald-300' : 'text-red-300'">
              Primary {{ status.online ? 'ONLINE' : 'OFFLINE' }}
            </div>
            <div v-if="status.ip" class="text-xs text-slate-500">{{ status.ip }}</div>
          </div>
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

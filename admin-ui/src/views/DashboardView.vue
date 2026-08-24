<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getStats, listCarriers, listClients, reloadCarriers, reloadClients } from '../api'
import type { StatsResponse } from '../api/types'
import { useToastStore } from '../stores/toast'
import { errorMessage, formatDate } from '../utils/format'

const toasts = useToastStore()
const stats = ref<StatsResponse | null>(null)
const clientCount = ref<number | null>(null)
const carrierCount = ref<number | null>(null)
const loading = ref(true)
const reloading = ref(false)

async function load() {
  loading.value = true
  try {
    const [s, clients, carriers] = await Promise.all([getStats(), listClients(), listCarriers()])
    stats.value = s
    clientCount.value = clients?.length ?? 0
    carrierCount.value = carriers?.length ?? 0
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function reloadAll() {
  reloading.value = true
  try {
    await reloadClients()
    await reloadCarriers()
    toasts.success('Clients and carriers reloaded.')
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    reloading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="mb-6 flex items-center justify-between">
      <h1 class="text-xl font-bold text-slate-100">Dashboard</h1>
      <div class="flex gap-2">
        <button
          class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="load"
        >
          Refresh
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="reloading"
          @click="reloadAll"
        >
          {{ reloading ? 'Reloading…' : 'Reload clients + carriers' }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="text-sm text-slate-500">Loading…</div>

    <template v-else>
      <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
        <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
          <div class="text-xs text-slate-500">Clients</div>
          <div class="mt-1 text-2xl font-bold text-slate-100">{{ clientCount ?? '—' }}</div>
        </div>
        <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
          <div class="text-xs text-slate-500">Carriers</div>
          <div class="mt-1 text-2xl font-bold text-slate-100">{{ carrierCount ?? '—' }}</div>
        </div>
        <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
          <div class="text-xs text-slate-500">SMPP sessions</div>
          <div class="mt-1 text-2xl font-bold" :class="stats?.smpp_connected_clients ? 'text-emerald-400' : 'text-slate-100'">
            {{ stats?.smpp_connected_clients ?? 0 }}
          </div>
        </div>
        <div class="rounded-lg border border-slate-800 bg-slate-900 p-4">
          <div class="text-xs text-slate-500">MM4 sessions</div>
          <div class="mt-1 text-2xl font-bold" :class="stats?.mm4_connected_clients ? 'text-emerald-400' : 'text-slate-100'">
            {{ stats?.mm4_connected_clients ?? 0 }}
          </div>
        </div>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-2">
        <div class="rounded-lg border border-slate-800 bg-slate-900">
          <h2 class="border-b border-slate-800 px-4 py-3 text-sm font-semibold text-slate-100">Connected SMPP clients</h2>
          <div v-if="!stats?.smpp_clients?.length" class="px-4 py-6 text-sm text-slate-500">No SMPP clients connected.</div>
          <table v-else class="w-full text-sm">
            <thead>
              <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
                <th class="px-4 py-2 font-medium">Username</th>
                <th class="px-4 py-2 font-medium">IP</th>
                <th class="px-4 py-2 font-medium">Last seen</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in stats.smpp_clients" :key="c.username + c.ip_address" class="border-b border-slate-800/50">
                <td class="px-4 py-2 text-slate-200">{{ c.username }}</td>
                <td class="px-4 py-2 text-slate-400">{{ c.ip_address }}</td>
                <td class="px-4 py-2 text-slate-400">{{ formatDate(c.last_seen) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="rounded-lg border border-slate-800 bg-slate-900">
          <h2 class="border-b border-slate-800 px-4 py-3 text-sm font-semibold text-slate-100">Connected MM4 clients</h2>
          <div v-if="!stats?.mm4_clients?.length" class="px-4 py-6 text-sm text-slate-500">No MM4 clients connected.</div>
          <table v-else class="w-full text-sm">
            <thead>
              <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
                <th class="px-4 py-2 font-medium">Username</th>
                <th class="px-4 py-2 font-medium">Sessions</th>
                <th class="px-4 py-2 font-medium">Last activity</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in stats.mm4_clients" :key="c.client_id" class="border-b border-slate-800/50">
                <td class="px-4 py-2 text-slate-200">{{ c.username }}</td>
                <td class="px-4 py-2 text-slate-400">{{ c.active_sessions }}</td>
                <td class="px-4 py-2 text-slate-400">{{ formatDate(c.last_activity_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getStats, listCarriers, listClients, reloadCarriers, reloadClients } from '../api'
import type { Client, StatsResponse } from '../api/types'
import { useToastStore } from '../stores/toast'
import { errorMessage, formatDate } from '../utils/format'

interface ConnectedClient {
  username: string
  client?: Client
  smpp?: { ip_address: string; last_seen: string }
  mm4?: { active_sessions: number; last_activity_at: string }
}

const toasts = useToastStore()
const stats = ref<StatsResponse | null>(null)
const clients = ref<Client[]>([])
const carrierCount = ref<number | null>(null)
const loading = ref(true)
const reloading = ref(false)

const connectedClients = computed<ConnectedClient[]>(() => {
  const byUsername = new Map(clients.value.map((c) => [c.username, c]))
  const map = new Map<string, ConnectedClient>()
  for (const s of stats.value?.smpp_clients ?? []) {
    const entry = map.get(s.username) ?? { username: s.username }
    entry.smpp = { ip_address: s.ip_address, last_seen: s.last_seen }
    entry.client = byUsername.get(s.username)
    map.set(s.username, entry)
  }
  for (const m of stats.value?.mm4_clients ?? []) {
    const entry = map.get(m.username) ?? { username: m.username }
    entry.mm4 = { active_sessions: m.active_sessions, last_activity_at: m.last_activity_at }
    entry.client = byUsername.get(m.username)
    map.set(m.username, entry)
  }
  return [...map.values()].sort((a, b) => a.username.localeCompare(b.username))
})

const disconnectedClients = computed(() => {
  const online = new Set(connectedClients.value.map((c) => c.username))
  return clients.value.filter((c) => !online.has(c.username))
})

async function load() {
  loading.value = true
  try {
    const [s, clientList, carriers] = await Promise.all([getStats(), listClients(), listCarriers()])
    stats.value = s
    clients.value = clientList ?? []
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
    await load()
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
          <div class="mt-1 text-2xl font-bold text-slate-100">{{ clients.length }}</div>
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

      <!-- Connected clients: single merged list with per-feature badges -->
      <div class="mt-6 rounded-lg border border-slate-800 bg-slate-900">
        <h2 class="border-b border-slate-800 px-4 py-3 text-sm font-semibold text-slate-100">
          Connected clients ({{ connectedClients.length }})
        </h2>
        <div v-if="!connectedClients.length" class="px-4 py-6 text-sm text-slate-500">No clients connected.</div>
        <div v-else class="divide-y divide-slate-800/50">
          <div
            v-for="c in connectedClients"
            :key="c.username"
            class="flex items-center justify-between gap-4 px-4 py-2.5"
          >
            <div class="flex min-w-0 items-center gap-3">
              <RouterLink
                v-if="c.client"
                :to="{ name: 'client-detail', params: { id: c.client.id } }"
                class="truncate text-sm font-medium text-slate-200 hover:text-indigo-300"
              >
                {{ c.client.name || c.username }}
              </RouterLink>
              <span v-else class="truncate text-sm font-medium text-slate-200">{{ c.username }}</span>
              <span v-if="c.client?.name && c.client.name !== c.username" class="truncate text-xs text-slate-500">
                {{ c.username }}
              </span>
              <span class="flex shrink-0 gap-1.5">
                <span
                  class="rounded-full px-2 py-0.5 text-xs font-medium"
                  :class="c.smpp ? 'bg-emerald-900/60 text-emerald-300' : 'bg-slate-800 text-slate-600'"
                  :title="c.smpp ? 'SMPP connected' : 'SMPP not connected'"
                >
                  SMPP
                </span>
                <span
                  class="rounded-full px-2 py-0.5 text-xs font-medium"
                  :class="c.mm4 ? 'bg-sky-900/60 text-sky-300' : 'bg-slate-800 text-slate-600'"
                  :title="c.mm4 ? 'MM4 connected' : 'MM4 not connected'"
                >
                  MM4
                </span>
              </span>
            </div>
            <div class="flex shrink-0 items-center gap-4 text-xs text-slate-500">
              <span v-if="c.smpp">{{ c.smpp.ip_address }} · {{ formatDate(c.smpp.last_seen) }}</span>
              <span v-if="c.mm4">
                {{ c.mm4.active_sessions }} session(s) · {{ formatDate(c.mm4.last_activity_at) }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Clients with no active SMPP or MM4 session -->
      <div class="mt-4 rounded-lg border border-slate-800 bg-slate-900">
        <h2 class="border-b border-slate-800 px-4 py-3 text-sm font-semibold text-slate-100">
          Not connected ({{ disconnectedClients.length }})
        </h2>
        <div v-if="!disconnectedClients.length" class="px-4 py-6 text-sm text-slate-500">
          All clients are connected.
        </div>
        <div v-else class="divide-y divide-slate-800/50">
          <div
            v-for="c in disconnectedClients"
            :key="c.id"
            class="flex items-center justify-between gap-4 px-4 py-2.5"
          >
            <div class="flex min-w-0 items-center gap-3">
              <RouterLink
                :to="{ name: 'client-detail', params: { id: c.id } }"
                class="truncate text-sm font-medium text-slate-200 hover:text-indigo-300"
              >
                {{ c.name || c.username }}
              </RouterLink>
              <span v-if="c.name && c.name !== c.username" class="truncate text-xs text-slate-500">
                {{ c.username }}
              </span>
            </div>
            <span class="flex shrink-0 gap-1.5">
              <span
                class="rounded-full bg-slate-800 px-2 py-0.5 text-xs font-medium text-slate-600"
                title="SMPP not connected"
              >
                SMPP
              </span>
              <span
                class="rounded-full bg-slate-800 px-2 py-0.5 text-xs font-medium text-slate-600"
                title="MM4 not connected"
              >
                MM4
              </span>
            </span>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

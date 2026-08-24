<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from './stores/auth'
import ToastHost from './components/ToastHost.vue'
import ConfirmHost from './components/ConfirmHost.vue'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const nav = [
  { name: 'dashboard', label: 'Dashboard', to: '/' },
  { name: 'clients', label: 'Clients', to: '/clients' },
  { name: 'carriers', label: 'Carriers', to: '/carriers' },
]

const showShell = computed(() => auth.isConnected && route.name !== 'connect')

function disconnect() {
  auth.disconnect()
  router.push({ name: 'connect' })
}
</script>

<template>
  <div class="min-h-screen bg-slate-950 text-slate-200">
    <header v-if="showShell" class="border-b border-slate-800 bg-slate-900">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-4 py-3">
        <div class="flex items-center gap-6">
          <span class="text-sm font-bold tracking-wide text-slate-100">GOMSGGW Admin</span>
          <nav class="flex gap-1">
            <RouterLink
              v-for="item in nav"
              :key="item.name"
              :to="item.to"
              class="rounded-md px-3 py-1.5 text-sm text-slate-400 hover:bg-slate-800 hover:text-slate-100"
              :class="{ 'bg-slate-800 text-slate-100': route.name === item.name || (item.name === 'clients' && route.name === 'client-detail') }"
            >
              {{ item.label }}
            </RouterLink>
          </nav>
        </div>
        <div class="flex items-center gap-3 text-xs text-slate-500">
          <span class="max-w-64 truncate" :title="auth.displayUrl">{{ auth.displayUrl }}</span>
          <button
            class="rounded-md border border-slate-700 px-2.5 py-1 text-slate-400 hover:bg-slate-800 hover:text-slate-200"
            @click="disconnect"
          >
            Disconnect
          </button>
        </div>
      </div>
    </header>

    <main :class="showShell ? 'mx-auto max-w-7xl px-4 py-6' : ''">
      <RouterView />
    </main>

    <ToastHost />
    <ConfirmHost />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { getStats } from '../api'
import { errorMessage } from '../utils/format'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const baseUrl = ref('')
const apiKey = ref('')
const showKey = ref(false)
const testing = ref(false)
const error = ref('')

async function connect() {
  error.value = ''
  if (!apiKey.value.trim()) {
    error.value = 'Admin master key is required.'
    return
  }
  testing.value = true
  try {
    auth.connect(baseUrl.value, apiKey.value)
    // Validate credentials before letting the user in.
    await getStats()
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    router.push(redirect)
  } catch (e) {
    error.value = errorMessage(e)
    auth.disconnect()
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center px-4">
    <div class="w-full max-w-md rounded-xl border border-slate-800 bg-slate-900 p-8 shadow-2xl">
      <h1 class="text-lg font-bold text-slate-100">GOMSGGW Admin</h1>
      <p class="mt-1 text-sm text-slate-400">Connect to a gateway with the admin master key.</p>

      <form class="mt-6 space-y-4" @submit.prevent="connect">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400" for="base-url">Gateway API URL</label>
          <input
            id="base-url"
            v-model="baseUrl"
            type="text"
            placeholder="Same origin (leave blank) or https://gateway.example.com"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:border-indigo-500 focus:outline-none"
          />
          <p class="mt-1 text-xs text-slate-600">
            Leave blank when this panel is served behind the bundled nginx proxy.
          </p>
        </div>

        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400" for="api-key">Admin master key</label>
          <div class="relative">
            <input
              id="api-key"
              v-model="apiKey"
              :type="showKey ? 'text' : 'password'"
              placeholder="API_KEY"
              autocomplete="off"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 pr-16 text-sm text-slate-100 placeholder-slate-600 focus:border-indigo-500 focus:outline-none"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-slate-500 hover:text-slate-300"
              @click="showKey = !showKey"
            >
              {{ showKey ? 'Hide' : 'Show' }}
            </button>
          </div>
          <p class="mt-1 text-xs text-slate-600">
            Stored in sessionStorage only — cleared when this tab closes.
          </p>
        </div>

        <p v-if="error" class="rounded-md border border-red-800 bg-red-950 px-3 py-2 text-sm text-red-300">
          {{ error }}
        </p>

        <button
          type="submit"
          :disabled="testing"
          class="w-full rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          {{ testing ? 'Testing connection…' : 'Connect' }}
        </button>
      </form>
    </div>
  </div>
</template>

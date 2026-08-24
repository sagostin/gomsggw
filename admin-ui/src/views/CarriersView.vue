<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { createCarrier, listCarriers, reloadCarriers } from '../api'
import type { Carrier } from '../api/types'
import AppModal from '../components/AppModal.vue'
import { useToastStore } from '../stores/toast'
import { errorMessage } from '../utils/format'

const toasts = useToastStore()
const carriers = ref<Carrier[]>([])
const loading = ref(true)
const showCreate = ref(false)
const creating = ref(false)
const reloading = ref(false)

const form = ref({
  name: '',
  type: 'telnyx',
  username: '',
  password: '',
  sms_limit: 600000,
  mms_limit: 1048576,
})

const credentialLabels: Record<string, { username: string; password: string; passwordOptional?: boolean }> = {
  telnyx: { username: 'API Key', password: 'API Secret', passwordOptional: true },
  twilio: { username: 'Account SID', password: 'Auth Token' },
  bandwidth: { username: 'Username / API Key', password: 'Password / Secret' },
  plivo: { username: 'Username / API Key', password: 'Password / Secret' },
}

async function load() {
  loading.value = true
  try {
    carriers.value = (await listCarriers()) ?? []
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function submitCreate() {
  creating.value = true
  try {
    await createCarrier({ ...form.value })
    toasts.success(`Carrier "${form.value.name}" created.`)
    showCreate.value = false
    form.value = { name: '', type: 'telnyx', username: '', password: '', sms_limit: 600000, mms_limit: 1048576 }
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    creating.value = false
  }
}

async function reload() {
  reloading.value = true
  try {
    await reloadCarriers()
    toasts.success('Carriers reloaded.')
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
      <h1 class="text-xl font-bold text-slate-100">Carriers</h1>
      <div class="flex gap-2">
        <button
          class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800 disabled:opacity-50"
          :disabled="reloading"
          @click="reload"
        >
          {{ reloading ? 'Reloading…' : 'Reload carriers' }}
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
          @click="showCreate = true"
        >
          Add carrier
        </button>
      </div>
    </div>

    <div class="rounded-lg border border-slate-800 bg-slate-900">
      <div v-if="loading" class="px-4 py-6 text-sm text-slate-500">Loading…</div>
      <div v-else-if="!carriers.length" class="px-4 py-6 text-sm text-slate-500">No carriers found.</div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
            <th class="px-4 py-2 font-medium">Name</th>
            <th class="px-4 py-2 font-medium">Type</th>
            <th class="px-4 py-2 font-medium">UUID</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in carriers" :key="c.id" class="border-b border-slate-800/50">
            <td class="px-4 py-2 text-slate-200">{{ c.name }}</td>
            <td class="px-4 py-2 text-slate-400">{{ c.type }}</td>
            <td class="px-4 py-2 font-mono text-xs text-slate-500">{{ c.uuid || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <AppModal v-if="showCreate" title="Add carrier" @close="showCreate = false">
      <form class="space-y-4" @submit.prevent="submitCreate">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Name</label>
          <input
            v-model="form.name"
            required
            placeholder="e.g. telnyx_prod"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Type</label>
          <select
            v-model="form.type"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option value="telnyx">telnyx</option>
            <option value="twilio">twilio</option>
            <option value="bandwidth">bandwidth</option>
            <option value="plivo">plivo</option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">{{ credentialLabels[form.type]?.username }}</label>
          <input
            v-model="form.username"
            required
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">
            {{ credentialLabels[form.type]?.password }}
            <span v-if="credentialLabels[form.type]?.passwordOptional" class="text-slate-600">(optional)</span>
          </label>
          <input
            v-model="form.password"
            type="password"
            :required="!credentialLabels[form.type]?.passwordOptional"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">SMS size limit (bytes)</label>
            <input
              v-model.number="form.sms_limit"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">MMS size limit (bytes)</label>
            <input
              v-model.number="form.mms_limit"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
        </div>
      </form>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showCreate = false"
        >
          Cancel
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="creating"
          @click="submitCreate"
        >
          {{ creating ? 'Creating…' : 'Create carrier' }}
        </button>
      </template>
    </AppModal>
  </div>
</template>

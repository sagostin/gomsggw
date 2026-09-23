<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { changeClientPassword, listClients, updateClient } from '../api'
import type { Client } from '../api/types'
import AppModal from '../components/AppModal.vue'
import NumbersTab from '../components/client/NumbersTab.vue'
import SettingsTab from '../components/client/SettingsTab.vue'
import ApiKeysTab from '../components/client/ApiKeysTab.vue'
import FailoversTab from '../components/client/FailoversTab.vue'
import StatusTab from '../components/client/StatusTab.vue'
import { useToastStore } from '../stores/toast'
import { generatePassword } from '../utils/password'
import { errorMessage } from '../utils/format'

const props = defineProps<{ id: string }>()
const clientId = computed(() => Number(props.id))

const toasts = useToastStore()
const client = ref<Client | null>(null)
const allClients = ref<Client[]>([])
const loading = ref(true)
const activeTab = ref<'numbers' | 'settings' | 'apikeys' | 'failover' | 'status'>('numbers')

const showPassword = ref(false)
const newPassword = ref('')
const changingPassword = ref(false)
const justGenerated = ref('')

const showEdit = ref(false)
const editForm = ref({ name: '', type: 'legacy', address: '' })
const savingEdit = ref(false)

const isLegacy = computed(() => !client.value?.type || client.value.type === 'legacy')

const tabs = computed(() => {
  const list = [
    { key: 'numbers', label: 'Numbers' },
    { key: 'settings', label: 'Settings' },
    { key: 'apikeys', label: 'API Keys' },
    { key: 'failover', label: 'Failover' },
  ] as { key: typeof activeTab.value; label: string }[]
  // Web clients can't hold SMPP/MM4 sessions, so the status tab is irrelevant.
  if (isLegacy.value) list.push({ key: 'status', label: 'Legacy Status' })
  return list
})

// If the type is switched to web while the status tab is open, bounce back to numbers.
watch(isLegacy, (legacy) => {
  if (!legacy && activeTab.value === 'status') activeTab.value = 'numbers'
})

async function load(background = false) {
  if (!background) loading.value = true
  try {
    allClients.value = (await listClients()) ?? []
    client.value = allClients.value.find((c) => c.id === clientId.value) ?? null
    if (!client.value) toasts.error(`Client ${props.id} not found.`)
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function openPasswordModal() {
  newPassword.value = ''
  justGenerated.value = ''
  showPassword.value = true
}

async function submitPassword() {
  if (!newPassword.value) {
    newPassword.value = generatePassword()
    justGenerated.value = newPassword.value
  }
  changingPassword.value = true
  try {
    await changeClientPassword(clientId.value, newPassword.value)
    toasts.success('Password updated.')
    if (!justGenerated.value) showPassword.value = false
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    changingPassword.value = false
  }
}

async function copyGenerated() {
  await navigator.clipboard.writeText(justGenerated.value)
  toasts.success('Password copied to clipboard.')
}

function openEdit() {
  if (!client.value) return
  editForm.value = {
    name: client.value.name || '',
    type: client.value.type || 'legacy',
    address: client.value.address || '',
  }
  showEdit.value = true
}

async function submitEdit() {
  if (!client.value) return
  savingEdit.value = true
  try {
    await updateClient(clientId.value, { ...editForm.value })
    toasts.success('Client updated.')
    showEdit.value = false
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    savingEdit.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div v-if="loading" class="text-sm text-slate-500">Loading…</div>
    <div v-else-if="!client" class="text-sm text-slate-500">
      Client not found.
      <RouterLink to="/clients" class="text-indigo-400 hover:underline">Back to clients</RouterLink>
    </div>

    <template v-else>
      <div class="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-3">
            <h1 class="text-xl font-bold text-slate-100">{{ client.username }}</h1>
            <span
              class="rounded-full px-2 py-0.5 text-xs"
              :class="client.type === 'web' ? 'bg-sky-900/60 text-sky-300' : 'bg-slate-800 text-slate-300'"
            >
              {{ client.type || 'legacy' }}
            </span>
          </div>
          <p class="mt-1 text-sm text-slate-500">
            ID {{ client.id }} · {{ client.name || 'no display name' }}
            <span v-if="client.address"> · {{ client.address }}</span>
          </p>
        </div>
        <div class="flex gap-2">
          <button
            class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
            @click="openEdit"
          >
            Edit client
          </button>
          <button
            class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
            @click="openPasswordModal"
          >
            Change password
          </button>
        </div>
      </div>

      <div class="mb-4 flex gap-1 border-b border-slate-800">
        <button
          v-for="t in tabs"
          :key="t.key"
          class="-mb-px border-b-2 px-4 py-2 text-sm"
          :class="
            activeTab === t.key
              ? 'border-indigo-500 text-slate-100'
              : 'border-transparent text-slate-500 hover:text-slate-300'
          "
          @click="activeTab = t.key"
        >
          {{ t.label }}
        </button>
      </div>

      <NumbersTab v-if="activeTab === 'numbers'" :client="client" @changed="load(true)" />
      <SettingsTab v-else-if="activeTab === 'settings'" :client="client" @changed="load(true)" />
      <ApiKeysTab v-else-if="activeTab === 'apikeys'" :client="client" />
      <FailoversTab v-else-if="activeTab === 'failover'" :client="client" :all-clients="allClients" @changed="load(true)" />
      <StatusTab v-else :client="client" />
    </template>

    <AppModal v-if="showEdit" :title="`Edit ${client?.username ?? 'client'}`" @close="showEdit = false">
      <form class="space-y-4" @submit.prevent="submitEdit">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Display name</label>
          <input
            v-model="editForm.name"
            placeholder="Company name"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Type</label>
          <select
            v-model="editForm.type"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option value="legacy">legacy (SMPP/MM4)</option>
            <option value="web">web (REST API/Webhooks)</option>
          </select>
          <p class="mt-1 text-xs text-slate-600">
            Web clients connect via the REST API only — they can't bind SMPP or MM4 sessions.
          </p>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">
            Address (IP or hostname)
            <span v-if="editForm.type === 'legacy'" class="text-red-400">*</span>
            <span v-else class="text-slate-600">(optional)</span>
          </label>
          <input
            v-model="editForm.address"
            :required="editForm.type === 'legacy'"
            placeholder="e.g. 203.0.113.10 or mm4.example.com"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
          <p v-if="editForm.type === 'legacy'" class="mt-1 text-xs text-slate-600">
            Required for legacy clients — used for the MM4 auth/validation and delivery. Hostnames are resolved via DNS.
          </p>
        </div>
      </form>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showEdit = false"
        >
          Cancel
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="savingEdit"
          @click="submitEdit"
        >
          {{ savingEdit ? 'Saving…' : 'Save' }}
        </button>
      </template>
    </AppModal>

    <AppModal v-if="showPassword" title="Change password" @close="showPassword = false">
      <div v-if="justGenerated" class="mb-4 rounded-md border border-amber-700 bg-amber-950 p-3">
        <p class="text-xs font-medium text-amber-300">Generated password — save it now, it won't be shown again:</p>
        <div class="mt-2 flex items-center gap-2">
          <code class="flex-1 break-all rounded bg-slate-950 px-2 py-1 font-mono text-xs text-amber-200">{{ justGenerated }}</code>
          <button
            class="shrink-0 rounded-md border border-amber-700 px-2 py-1 text-xs text-amber-300 hover:bg-amber-900"
            @click="copyGenerated"
          >
            Copy
          </button>
        </div>
      </div>
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-400">New password</label>
        <div class="flex gap-2">
          <input
            v-model="newPassword"
            type="text"
            :disabled="!!justGenerated"
            placeholder="Leave blank to auto-generate"
            class="flex-1 rounded-md border border-slate-700 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
          <button
            class="shrink-0 rounded-md border border-slate-600 px-3 py-2 text-xs text-slate-300 hover:bg-slate-800 disabled:opacity-50"
            :disabled="!!justGenerated"
            @click="newPassword = generatePassword()"
          >
            Generate
          </button>
        </div>
      </div>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showPassword = false"
        >
          {{ justGenerated ? 'Done' : 'Cancel' }}
        </button>
        <button
          v-if="!justGenerated"
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="changingPassword"
          @click="submitPassword"
        >
          {{ changingPassword ? 'Updating…' : 'Update password' }}
        </button>
      </template>
    </AppModal>
  </div>
</template>

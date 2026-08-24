<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { createClient, deleteClient, listClients } from '../api'
import type { Client } from '../api/types'
import AppModal from '../components/AppModal.vue'
import { useConfirmStore } from '../stores/confirm'
import { useToastStore } from '../stores/toast'
import { generatePassword } from '../utils/password'
import { errorMessage } from '../utils/format'

const router = useRouter()
const toasts = useToastStore()
const confirm = useConfirmStore()

const clients = ref<Client[]>([])
const loading = ref(true)
const search = ref('')
const showCreate = ref(false)
const creating = ref(false)

const form = ref({
  username: '',
  password: '',
  name: '',
  type: 'legacy',
  address: '',
})
const generatedPassword = ref('')

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return clients.value
  return clients.value.filter(
    (c) =>
      c.username.toLowerCase().includes(q) ||
      (c.name || '').toLowerCase().includes(q) ||
      String(c.id) === q,
  )
})

async function load() {
  loading.value = true
  try {
    clients.value = (await listClients()) ?? []
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.value = { username: '', password: '', name: '', type: 'legacy', address: '' }
  generatedPassword.value = ''
  showCreate.value = true
}

function fillGeneratedPassword() {
  form.value.password = generatePassword()
}

async function submitCreate() {
  if (!form.value.password) {
    form.value.password = generatePassword()
    generatedPassword.value = form.value.password
  }
  creating.value = true
  try {
    const payload: Record<string, unknown> = { ...form.value }
    if (!payload.address) delete payload.address
    const created = await createClient(payload as never)
    if (generatedPassword.value) {
      // keep modal open so the user can copy the generated password
    } else {
      showCreate.value = false
    }
    toasts.success(`Client "${created.username}" created (ID ${created.id}).`)
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    creating.value = false
  }
}

function closeCreate() {
  showCreate.value = false
  generatedPassword.value = ''
}

async function copyGenerated() {
  await navigator.clipboard.writeText(generatedPassword.value)
  toasts.success('Password copied to clipboard.')
}

async function removeClient(c: Client) {
  const ok = await confirm.ask({
    title: 'Delete client',
    message: `Delete client "${c.username}" (ID ${c.id}) and all its numbers? This cannot be undone.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteClient(c.id)
    toasts.success(`Client "${c.username}" deleted.`)
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="mb-6 flex items-center justify-between">
      <h1 class="text-xl font-bold text-slate-100">Clients</h1>
      <button
        class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
        @click="openCreate"
      >
        Create client
      </button>
    </div>

    <div class="mb-4">
      <input
        v-model="search"
        type="text"
        placeholder="Search by username, name, or ID…"
        class="w-full max-w-sm rounded-md border border-slate-700 bg-slate-900 px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:border-indigo-500 focus:outline-none"
      />
    </div>

    <div class="rounded-lg border border-slate-800 bg-slate-900">
      <div v-if="loading" class="px-4 py-6 text-sm text-slate-500">Loading…</div>
      <div v-else-if="!filtered.length" class="px-4 py-6 text-sm text-slate-500">No clients found.</div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
            <th class="px-4 py-2 font-medium">ID</th>
            <th class="px-4 py-2 font-medium">Username</th>
            <th class="px-4 py-2 font-medium">Name</th>
            <th class="px-4 py-2 font-medium">Type</th>
            <th class="px-4 py-2 font-medium">Numbers</th>
            <th class="px-4 py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="c in filtered"
            :key="c.id"
            class="cursor-pointer border-b border-slate-800/50 hover:bg-slate-800/40"
            @click="router.push({ name: 'client-detail', params: { id: c.id } })"
          >
            <td class="px-4 py-2 text-slate-500">{{ c.id }}</td>
            <td class="px-4 py-2 font-medium text-slate-200">{{ c.username }}</td>
            <td class="px-4 py-2 text-slate-400">{{ c.name || '—' }}</td>
            <td class="px-4 py-2">
              <span
                class="rounded-full px-2 py-0.5 text-xs"
                :class="c.type === 'web' ? 'bg-sky-900/60 text-sky-300' : 'bg-slate-800 text-slate-300'"
              >
                {{ c.type || 'legacy' }}
              </span>
            </td>
            <td class="px-4 py-2 text-slate-400">{{ c.numbers?.length ?? 0 }}</td>
            <td class="px-4 py-2 text-right">
              <button
                class="rounded-md border border-red-900 px-2 py-1 text-xs text-red-400 hover:bg-red-950"
                @click.stop="removeClient(c)"
              >
                Delete
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <AppModal v-if="showCreate" title="Create client" @close="closeCreate">
      <div v-if="generatedPassword" class="mb-4 rounded-md border border-amber-700 bg-amber-950 p-3">
        <p class="text-xs font-medium text-amber-300">Generated password — save it now, it won't be shown again:</p>
        <div class="mt-2 flex items-center gap-2">
          <code class="flex-1 break-all rounded bg-slate-950 px-2 py-1 font-mono text-xs text-amber-200">{{ generatedPassword }}</code>
          <button
            class="shrink-0 rounded-md border border-amber-700 px-2 py-1 text-xs text-amber-300 hover:bg-amber-900"
            @click="copyGenerated"
          >
            Copy
          </button>
        </div>
      </div>

      <form class="space-y-4" @submit.prevent="submitCreate">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Username</label>
          <input
            v-model="form.username"
            required
            :disabled="!!generatedPassword"
            placeholder="e.g. tops_zultys"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Password</label>
          <div class="flex gap-2">
            <input
              v-model="form.password"
              type="text"
              :disabled="!!generatedPassword"
              placeholder="Leave blank to auto-generate"
              class="flex-1 rounded-md border border-slate-700 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
            />
            <button
              type="button"
              class="shrink-0 rounded-md border border-slate-600 px-3 py-2 text-xs text-slate-300 hover:bg-slate-800 disabled:opacity-50"
              :disabled="!!generatedPassword"
              @click="fillGeneratedPassword"
            >
              Generate
            </button>
          </div>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Display name</label>
          <input
            v-model="form.name"
            :disabled="!!generatedPassword"
            placeholder="Company name"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Type</label>
          <select
            v-model="form.type"
            :disabled="!!generatedPassword"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          >
            <option value="legacy">legacy (SMPP/MM4)</option>
            <option value="web">web (REST API/Webhooks)</option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">
            Address (IP or hostname)
            <span v-if="form.type === 'legacy'" class="text-red-400">*</span>
            <span v-else class="text-slate-600">(optional)</span>
          </label>
          <input
            v-model="form.address"
            :required="form.type === 'legacy'"
            :disabled="!!generatedPassword"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
          <p v-if="form.type === 'legacy'" class="mt-1 text-xs text-slate-600">
            Required for legacy clients — used for the SMPP ACL and MM4 delivery.
          </p>
        </div>
      </form>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="closeCreate"
        >
          {{ generatedPassword ? 'Done' : 'Cancel' }}
        </button>
        <button
          v-if="!generatedPassword"
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="creating"
          @click="submitCreate"
        >
          {{ creating ? 'Creating…' : 'Create client' }}
        </button>
      </template>
    </AppModal>
  </div>
</template>

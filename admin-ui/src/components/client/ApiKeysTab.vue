<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { createApiKey, listApiKeys, revokeApiKey } from '../../api'
import type { ApiKeyCreateResponse, Client, TenantApiKey } from '../../api/types'
import AppModal from '../AppModal.vue'
import { useConfirmStore } from '../../stores/confirm'
import { useToastStore } from '../../stores/toast'
import { errorMessage, formatDate } from '../../utils/format'
import { formatNumber } from '../../utils/numbers'

const props = defineProps<{ client: Client }>()

const toasts = useToastStore()
const confirm = useConfirmStore()

const keys = ref<TenantApiKey[]>([])
const loading = ref(true)
const showCreate = ref(false)
const creating = ref(false)
const createdKey = ref<ApiKeyCreateResponse | null>(null)

const ALL_SCOPES = ['send', 'batch', 'usage']

const form = ref({
  name: '',
  scopes: [...ALL_SCOPES] as string[],
  rate_limit: 0,
  expires_in_days: 0,
  allowed_number_ids: [] as number[],
})

async function load() {
  loading.value = true
  try {
    keys.value = (await listApiKeys(props.client.id)) ?? []
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.value = { name: '', scopes: [...ALL_SCOPES], rate_limit: 0, expires_in_days: 0, allowed_number_ids: [] }
  createdKey.value = null
  showCreate.value = true
}

function toggleScope(scope: string) {
  const i = form.value.scopes.indexOf(scope)
  if (i >= 0) form.value.scopes.splice(i, 1)
  else form.value.scopes.push(scope)
}

function toggleNumber(id: number) {
  const i = form.value.allowed_number_ids.indexOf(id)
  if (i >= 0) form.value.allowed_number_ids.splice(i, 1)
  else form.value.allowed_number_ids.push(id)
}

async function submitCreate() {
  creating.value = true
  try {
    createdKey.value = await createApiKey(props.client.id, {
      name: form.value.name || 'API Key',
      scopes: form.value.scopes.join(','),
      rate_limit: form.value.rate_limit,
      expires_in_days: form.value.expires_in_days,
      allowed_number_ids: form.value.allowed_number_ids,
    })
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    creating.value = false
  }
}

async function copyRawKey() {
  if (createdKey.value?.key) {
    await navigator.clipboard.writeText(createdKey.value.key)
    toasts.success('API key copied to clipboard.')
  }
}

async function revoke(k: TenantApiKey) {
  const ok = await confirm.ask({
    title: 'Revoke API key',
    message: `Revoke key "${k.name}" (${k.key_prefix}…)? This cannot be undone.`,
    confirmLabel: 'Revoke',
    danger: true,
  })
  if (!ok) return
  try {
    await revokeApiKey(props.client.id, k.id)
    toasts.success('API key revoked.')
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="mb-4 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-slate-100">API keys ({{ keys.length }})</h2>
      <button
        class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
        @click="openCreate"
      >
        Create API key
      </button>
    </div>

    <div class="rounded-lg border border-slate-800 bg-slate-900">
      <div v-if="loading" class="px-4 py-6 text-sm text-slate-500">Loading…</div>
      <div v-else-if="!keys.length" class="px-4 py-6 text-sm text-slate-500">No API keys found.</div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
            <th class="px-4 py-2 font-medium">Name</th>
            <th class="px-4 py-2 font-medium">Prefix</th>
            <th class="px-4 py-2 font-medium">Scopes</th>
            <th class="px-4 py-2 font-medium">Numbers</th>
            <th class="px-4 py-2 font-medium">Expires</th>
            <th class="px-4 py-2 font-medium">Status</th>
            <th class="px-4 py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="k in keys" :key="k.id" class="border-b border-slate-800/50">
            <td class="px-4 py-2 text-slate-200">{{ k.name }}</td>
            <td class="px-4 py-2 font-mono text-xs text-slate-400">{{ k.key_prefix }}…</td>
            <td class="px-4 py-2 text-slate-400">{{ k.scopes }}</td>
            <td class="px-4 py-2 text-slate-400">
              {{ k.allowed_numbers?.length ? `${k.allowed_numbers.length} scoped` : 'all' }}
            </td>
            <td class="px-4 py-2 text-slate-400">{{ formatDate(k.expires_at) }}</td>
            <td class="px-4 py-2">
              <span
                class="rounded-full px-2 py-0.5 text-xs"
                :class="k.active ? 'bg-emerald-900/60 text-emerald-300' : 'bg-red-900/60 text-red-300'"
              >
                {{ k.active ? 'active' : 'revoked' }}
              </span>
            </td>
            <td class="px-4 py-2 text-right">
              <button
                v-if="k.active"
                class="rounded-md border border-red-900 px-2 py-1 text-xs text-red-400 hover:bg-red-950"
                @click="revoke(k)"
              >
                Revoke
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <AppModal v-if="showCreate" title="Create API key" @close="showCreate = false">
      <div v-if="createdKey" class="mb-4 rounded-md border border-amber-700 bg-amber-950 p-3">
        <p class="text-xs font-medium text-amber-300">Raw key — save it now, it will not be shown again:</p>
        <div class="mt-2 flex items-center gap-2">
          <code class="flex-1 break-all rounded bg-slate-950 px-2 py-1 font-mono text-xs text-amber-200">{{ createdKey.key }}</code>
          <button
            class="shrink-0 rounded-md border border-amber-700 px-2 py-1 text-xs text-amber-300 hover:bg-amber-900"
            @click="copyRawKey"
          >
            Copy
          </button>
        </div>
        <p class="mt-2 text-xs text-amber-400/80">
          Prefix {{ createdKey.key_prefix }} · scopes {{ createdKey.scopes }} · expires {{ formatDate(createdKey.expires_at) }}
        </p>
      </div>

      <div v-if="!createdKey" class="space-y-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Key name</label>
          <input
            v-model="form.name"
            placeholder="e.g. CSV Import App"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Scopes</label>
          <div class="flex gap-4">
            <label v-for="s in ALL_SCOPES" :key="s" class="flex items-center gap-2 text-sm text-slate-300">
              <input
                type="checkbox"
                class="accent-indigo-500"
                :checked="form.scopes.includes(s)"
                @change="toggleScope(s)"
              />
              {{ s }}
            </label>
          </div>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Rate limit (req/min, 0 = client limit)</label>
            <input
              v-model.number="form.rate_limit"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Expires in days (0 = never)</label>
            <input
              v-model.number="form.expires_in_days"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
        </div>
        <div v-if="client.numbers?.length">
          <label class="mb-1 block text-xs font-medium text-slate-400">
            Restrict to numbers <span class="text-slate-600">(none selected = all numbers)</span>
          </label>
          <div class="max-h-36 overflow-y-auto rounded-md border border-slate-800">
            <label
              v-for="n in client.numbers"
              :key="n.id"
              class="flex items-center gap-2 border-b border-slate-800/50 px-3 py-1.5 text-sm text-slate-300"
            >
              <input
                type="checkbox"
                class="accent-indigo-500"
                :checked="form.allowed_number_ids.includes(n.id)"
                @change="toggleNumber(n.id)"
              />
              {{ formatNumber(n.number) }}
              <span class="text-xs text-slate-500">{{ n.carrier }}</span>
            </label>
          </div>
        </div>
      </div>

      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showCreate = false"
        >
          {{ createdKey ? 'Done' : 'Cancel' }}
        </button>
        <button
          v-if="!createdKey"
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="creating || !form.scopes.length"
          @click="submitCreate"
        >
          {{ creating ? 'Creating…' : 'Create key' }}
        </button>
      </template>
    </AppModal>
  </div>
</template>

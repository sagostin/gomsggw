<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { addFailover, listFailovers, removeFailover, updateFailover } from '../../api'
import type { Client, Failover } from '../../api/types'
import AppModal from '../AppModal.vue'
import { useConfirmStore } from '../../stores/confirm'
import { useToastStore } from '../../stores/toast'
import { errorMessage } from '../../utils/format'

const props = defineProps<{ client: Client; allClients: Client[] }>()
const emit = defineEmits<{ changed: [] }>()

const toasts = useToastStore()
const confirm = useConfirmStore()

const failovers = ref<Failover[]>([])
const loading = ref(true)
const showAdd = ref(false)
const adding = ref(false)

const addForm = ref({ fallback_client_id: 0, priority: 0 })

const eligible = computed(() =>
  props.allClients.filter((c) => c.id !== props.client.id && (c.type || 'legacy') === 'legacy'),
)

async function load() {
  loading.value = true
  try {
    failovers.value = (await listFailovers(props.client.id)) ?? []
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function openAdd() {
  addForm.value = { fallback_client_id: eligible.value[0]?.id ?? 0, priority: 0 }
  showAdd.value = true
}

async function submitAdd() {
  if (!addForm.value.fallback_client_id) return
  adding.value = true
  try {
    await addFailover(props.client.id, addForm.value.fallback_client_id, addForm.value.priority)
    toasts.success('Failover added.')
    showAdd.value = false
    await load()
    emit('changed')
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    adding.value = false
  }
}

async function toggleEnabled(f: Failover) {
  try {
    await updateFailover(props.client.id, f.id, { enabled: !f.enabled })
    toasts.success(`Failover ${!f.enabled ? 'enabled' : 'disabled'}.`)
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  }
}

async function changePriority(f: Failover, event: Event) {
  const value = Number((event.target as HTMLInputElement).value)
  if (isNaN(value) || value === f.priority) return
  try {
    await updateFailover(props.client.id, f.id, { priority: value })
    toasts.success('Priority updated.')
    await load()
  } catch (e) {
    toasts.error(errorMessage(e))
  }
}

async function remove(f: Failover) {
  const ok = await confirm.ask({
    title: 'Remove failover',
    message: `Remove failover to "${f.fallback_client_username || f.fallback_client_id}"?`,
    confirmLabel: 'Remove',
    danger: true,
  })
  if (!ok) return
  try {
    await removeFailover(props.client.id, f.id)
    toasts.success('Failover removed.')
    await load()
    emit('changed')
  } catch (e) {
    toasts.error(errorMessage(e))
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="mb-4 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-slate-100">Failovers ({{ failovers.length }})</h2>
      <button
        class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        :disabled="!eligible.length"
        :title="eligible.length ? '' : 'No other legacy clients available'"
        @click="openAdd"
      >
        Add failover
      </button>
    </div>

    <div class="rounded-lg border border-slate-800 bg-slate-900">
      <div v-if="loading" class="px-4 py-6 text-sm text-slate-500">Loading…</div>
      <div v-else-if="!failovers.length" class="px-4 py-6 text-sm text-slate-500">No failovers configured.</div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
            <th class="px-4 py-2 font-medium">Fallback client</th>
            <th class="px-4 py-2 font-medium">Username</th>
            <th class="px-4 py-2 font-medium">Priority</th>
            <th class="px-4 py-2 font-medium">Enabled</th>
            <th class="px-4 py-2 font-medium">Online</th>
            <th class="px-4 py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="f in failovers" :key="f.id" class="border-b border-slate-800/50">
            <td class="px-4 py-2 text-slate-200">{{ f.fallback_client_name || '—' }}</td>
            <td class="px-4 py-2 text-slate-400">{{ f.fallback_client_username || f.fallback_client_id }}</td>
            <td class="px-4 py-2">
              <input
                type="number"
                :value="f.priority"
                class="w-20 rounded-md border border-slate-700 bg-slate-950 px-2 py-1 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                @change="changePriority(f, $event)"
              />
            </td>
            <td class="px-4 py-2">
              <button
                class="rounded-full px-2 py-0.5 text-xs"
                :class="f.enabled ? 'bg-emerald-900/60 text-emerald-300' : 'bg-slate-800 text-slate-500'"
                @click="toggleEnabled(f)"
              >
                {{ f.enabled ? 'enabled' : 'disabled' }}
              </button>
            </td>
            <td class="px-4 py-2">
              <span :class="f.fallback_online ? 'text-emerald-400' : 'text-red-400'">
                {{ f.fallback_online ? '● online' : '● offline' }}
              </span>
            </td>
            <td class="px-4 py-2 text-right">
              <button
                class="rounded-md border border-red-900 px-2 py-1 text-xs text-red-400 hover:bg-red-950"
                @click="remove(f)"
              >
                Remove
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <AppModal v-if="showAdd" title="Add failover" @close="showAdd = false">
      <div class="space-y-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Fallback client (legacy clients only)</label>
          <select
            v-model.number="addForm.fallback_client_id"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option v-for="c in eligible" :key="c.id" :value="c.id">
              {{ c.username }} — {{ c.name || 'no name' }} (ID {{ c.id }})
            </option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Priority (lower = tried first)</label>
          <input
            v-model.number="addForm.priority"
            type="number"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
      </div>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showAdd = false"
        >
          Cancel
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="adding || !addForm.fallback_client_id"
          @click="submitAdd"
        >
          {{ adding ? 'Adding…' : 'Add failover' }}
        </button>
      </template>
    </AppModal>
  </div>
</template>

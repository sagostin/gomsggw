<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  addNumber,
  deleteNumber,
  getAutoReply,
  listCarriers,
  updateAutoReply,
  updateNumber,
} from '../../api'
import type { AutoReplyConfig, Carrier, Client, ClientNumber } from '../../api/types'
import AppModal from '../AppModal.vue'
import { useConfirmStore } from '../../stores/confirm'
import { useToastStore } from '../../stores/toast'
import { formatNumber, parseNumbersCsv } from '../../utils/numbers'
import { errorMessage } from '../../utils/format'

const props = defineProps<{ client: Client }>()
const emit = defineEmits<{ changed: [] }>()

const toasts = useToastStore()
const confirm = useConfirmStore()

const carriers = ref<Carrier[]>([])

// --- Bulk add ---
const showAdd = ref(false)
const addCarrier = ref('telnyx')
const addRaw = ref('')
const addTag = ref('')
const addGroup = ref('')
const adding = ref(false)
const addResults = ref<{ num: string; status: 'pending' | 'added' | 'skipped' | 'failed'; detail?: string }[]>([])

const parsed = computed(() => parseNumbersCsv(addRaw.value))

// --- Edit number ---
const editing = ref<ClientNumber | null>(null)
const editForm = ref({ carrier: '', tag: '', group: '', webhook: '' })
const savingEdit = ref(false)

// --- Auto-reply ---
const arNumber = ref<ClientNumber | null>(null)
const arConfig = ref<AutoReplyConfig | null>(null)
const arForm = ref({ enabled: false, message: '', cooldown_secs: 60 })
const arLoading = ref(false)
const arSaving = ref(false)

// --- Bulk auto-reply ---
const selected = ref<Set<number>>(new Set())
const showBulkAr = ref(false)
const bulkArForm = ref({ enabled: true, message: '', cooldown_secs: 60 })
const bulkArRunning = ref(false)
const bulkArResults = ref<{ num: string; ok: boolean; detail?: string }[]>([])

const allSelected = computed(() => numbers.value.length > 0 && selected.value.size === numbers.value.length)

// Drop selected IDs that no longer exist after a reload.
watch(
  () => props.client.numbers,
  (nums) => {
    const ids = new Set((nums ?? []).map((n) => n.id))
    selected.value = new Set([...selected.value].filter((id) => ids.has(id)))
  },
)

function toggleSelect(id: number) {
  if (selected.value.has(id)) selected.value.delete(id)
  else selected.value.add(id)
  selected.value = new Set(selected.value)
}

function toggleSelectAll() {
  selected.value = allSelected.value ? new Set() : new Set(numbers.value.map((n) => n.id))
}

function openBulkAr() {
  bulkArForm.value = { enabled: true, message: '', cooldown_secs: 60 }
  bulkArResults.value = []
  showBulkAr.value = true
}

async function submitBulkAr() {
  bulkArRunning.value = true
  bulkArResults.value = []
  const targets = numbers.value.filter((n) => selected.value.has(n.id))
  for (const n of targets) {
    try {
      await updateAutoReply(n.id, {
        enabled: bulkArForm.value.enabled,
        message: bulkArForm.value.message,
        cooldown_secs: bulkArForm.value.cooldown_secs,
      })
      bulkArResults.value.push({ num: n.number, ok: true })
    } catch (e) {
      bulkArResults.value.push({ num: n.number, ok: false, detail: errorMessage(e) })
    }
  }
  bulkArRunning.value = false
  const failed = bulkArResults.value.filter((r) => !r.ok).length
  if (failed) toasts.error(`${bulkArResults.value.length - failed} updated, ${failed} failed.`)
  else toasts.success(`Auto-reply updated on ${bulkArResults.value.length} number(s).`)
  emit('changed')
}

const numbers = computed(() => props.client.numbers ?? [])

onMounted(async () => {
  try {
    carriers.value = (await listCarriers()) ?? []
    if (carriers.value.length && !carriers.value.some((c) => c.name === addCarrier.value)) {
      addCarrier.value = carriers.value[0]!.name
    }
  } catch {
    /* carrier list is a nice-to-have for the dropdown */
  }
})

function openAdd() {
  addRaw.value = ''
  addTag.value = ''
  addGroup.value = ''
  addResults.value = []
  showAdd.value = true
}

async function submitAdd() {
  const { valid } = parsed.value
  if (!valid.length) return
  adding.value = true
  const existing = new Set(numbers.value.map((n) => n.number))
  addResults.value = valid.map((num) => ({
    num,
    status: existing.has(num) ? 'skipped' : 'pending',
    detail: existing.has(num) ? 'already exists' : undefined,
  }))

  for (const r of addResults.value) {
    if (r.status !== 'pending') continue
    try {
      await addNumber(props.client.id, r.num, addCarrier.value, addTag.value || undefined, addGroup.value || undefined)
      r.status = 'added'
      existing.add(r.num)
    } catch (e) {
      const msg = errorMessage(e)
      if (msg.toLowerCase().includes('already exists')) {
        r.status = 'skipped'
        r.detail = 'already exists'
      } else {
        r.status = 'failed'
        r.detail = msg
      }
    }
  }
  adding.value = false
  const added = addResults.value.filter((r) => r.status === 'added').length
  const failed = addResults.value.filter((r) => r.status === 'failed').length
  if (failed) toasts.error(`${added} added, ${failed} failed.`)
  else toasts.success(`${added} number(s) added.`)
  emit('changed')
}

function openEdit(n: ClientNumber) {
  editing.value = n
  editForm.value = { carrier: n.carrier, tag: n.tag || '', group: n.group || '', webhook: n.webhook || '' }
}

async function submitEdit() {
  if (!editing.value) return
  savingEdit.value = true
  try {
    await updateNumber(props.client.id, editing.value.id, { ...editForm.value })
    toasts.success(`Number ${formatNumber(editing.value.number)} updated.`)
    editing.value = null
    emit('changed')
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    savingEdit.value = false
  }
}

async function removeNumber(n: ClientNumber) {
  const ok = await confirm.ask({
    title: 'Delete number',
    message: `Remove ${formatNumber(n.number)} from ${props.client.username}?`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteNumber(props.client.id, n.id)
    toasts.success('Number deleted.')
    emit('changed')
  } catch (e) {
    toasts.error(errorMessage(e))
  }
}

async function openAutoReply(n: ClientNumber) {
  arNumber.value = n
  arConfig.value = null
  arLoading.value = true
  try {
    const cfg = await getAutoReply(n.id)
    arConfig.value = cfg
    arForm.value = {
      enabled: cfg.enabled,
      message: cfg.message || '',
      cooldown_secs: cfg.cooldown_secs ?? 60,
    }
  } catch (e) {
    toasts.error(errorMessage(e))
    arNumber.value = null
  } finally {
    arLoading.value = false
  }
}

async function submitAutoReply() {
  if (!arNumber.value) return
  arSaving.value = true
  try {
    await updateAutoReply(arNumber.value.id, {
      enabled: arForm.value.enabled,
      message: arForm.value.message,
      cooldown_secs: arForm.value.cooldown_secs,
    })
    toasts.success('Auto-reply settings updated.')
    const cfg = await getAutoReply(arNumber.value.id)
    arConfig.value = cfg
    emit('changed')
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    arSaving.value = false
  }
}
</script>

<template>
  <div>
    <div class="mb-4 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-slate-100">Numbers ({{ numbers.length }})</h2>
      <div class="flex gap-2">
        <button
          v-if="selected.size"
          class="rounded-md border border-slate-700 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="openBulkAr"
        >
          Bulk auto-reply ({{ selected.size }})
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
          @click="openAdd"
        >
          Add numbers
        </button>
      </div>
    </div>

    <div class="rounded-lg border border-slate-800 bg-slate-900">
      <div v-if="!numbers.length" class="px-4 py-6 text-sm text-slate-500">No numbers configured.</div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-800 text-left text-xs text-slate-500">
            <th class="px-4 py-2 font-medium">
              <input type="checkbox" class="accent-indigo-500" :checked="allSelected" @change="toggleSelectAll" />
            </th>
            <th class="px-4 py-2 font-medium">Number</th>
            <th class="px-4 py-2 font-medium">Carrier</th>
            <th class="px-4 py-2 font-medium">Tag</th>
            <th class="px-4 py-2 font-medium">Group</th>
            <th class="px-4 py-2 font-medium">Auto-reply</th>
            <th class="px-4 py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="n in numbers" :key="n.id" class="border-b border-slate-800/50">
            <td class="px-4 py-2">
              <input
                type="checkbox"
                class="accent-indigo-500"
                :checked="selected.has(n.id)"
                @change="toggleSelect(n.id)"
              />
            </td>
            <td class="px-4 py-2 font-medium text-slate-200">{{ formatNumber(n.number) }}</td>
            <td class="px-4 py-2 text-slate-400">{{ n.carrier }}</td>
            <td class="px-4 py-2 text-slate-400">{{ n.tag || '—' }}</td>
            <td class="px-4 py-2 text-slate-400">{{ n.group || '—' }}</td>
            <td class="px-4 py-2">
              <span
                class="rounded-full px-2 py-0.5 text-xs"
                :class="n.settings?.auto_reply_enabled ? 'bg-emerald-900/60 text-emerald-300' : 'bg-slate-800 text-slate-500'"
              >
                {{ n.settings?.auto_reply_enabled ? 'on' : 'off' }}
              </span>
            </td>
            <td class="px-4 py-2 text-right">
              <div class="flex justify-end gap-2">
                <button
                  class="rounded-md border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:bg-slate-800"
                  @click="openAutoReply(n)"
                >
                  Auto-reply
                </button>
                <button
                  class="rounded-md border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:bg-slate-800"
                  @click="openEdit(n)"
                >
                  Edit
                </button>
                <button
                  class="rounded-md border border-red-900 px-2 py-1 text-xs text-red-400 hover:bg-red-950"
                  @click="removeNumber(n)"
                >
                  Delete
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Bulk add modal -->
    <AppModal v-if="showAdd" title="Add numbers" wide @close="showAdd = false">
      <div class="space-y-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Carrier</label>
          <select
            v-model="addCarrier"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option v-for="c in carriers" :key="c.id" :value="c.name">{{ c.name }}</option>
            <option v-if="!carriers.length" value="telnyx">telnyx</option>
          </select>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">
              Tag <span class="text-slate-600">(optional, applies to all)</span>
            </label>
            <input
              v-model="addTag"
              :disabled="adding"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">
              Group <span class="text-slate-600">(optional, applies to all)</span>
            </label>
            <input
              v-model="addGroup"
              :disabled="adding"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
            />
          </div>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">
            Numbers — comma-separated or one per line (E.164 OK, e.g. +12505551234)
          </label>
          <textarea
            v-model="addRaw"
            rows="6"
            :disabled="adding"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
          <p v-if="addRaw.trim()" class="mt-1 text-xs text-slate-500">
            {{ parsed.valid.length }} valid, {{ parsed.invalid.length }} invalid
          </p>
          <div v-if="parsed.rewritten.length" class="mt-2 max-h-24 overflow-y-auto text-xs text-slate-500">
            <div v-for="r in parsed.rewritten" :key="r.from">{{ r.from }} → {{ r.to }}</div>
          </div>
          <div v-if="parsed.invalid.length" class="mt-2 text-xs text-red-400">
            Invalid: {{ parsed.invalid.join(', ') }}
          </div>
        </div>

        <div v-if="addResults.length" class="max-h-48 overflow-y-auto rounded-md border border-slate-800">
          <div
            v-for="r in addResults"
            :key="r.num"
            class="flex items-center justify-between border-b border-slate-800/50 px-3 py-1.5 text-xs"
          >
            <span class="font-mono text-slate-300">{{ formatNumber(r.num) }}</span>
            <span
              :class="{
                'text-slate-500': r.status === 'pending',
                'text-emerald-400': r.status === 'added',
                'text-amber-400': r.status === 'skipped',
                'text-red-400': r.status === 'failed',
              }"
            >
              {{ r.status }}<span v-if="r.detail" class="text-slate-500"> — {{ r.detail }}</span>
            </span>
          </div>
        </div>
      </div>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showAdd = false"
        >
          Close
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="adding || !parsed.valid.length"
          @click="submitAdd"
        >
          {{ adding ? 'Adding…' : `Add ${parsed.valid.length} number(s)` }}
        </button>
      </template>
    </AppModal>

    <!-- Edit number modal -->
    <AppModal v-if="editing" :title="`Edit ${formatNumber(editing.number)}`" @close="editing = null">
      <div class="space-y-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Carrier</label>
          <select
            v-model="editForm.carrier"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option v-for="c in carriers" :key="c.id" :value="c.name">{{ c.name }}</option>
            <option v-if="!carriers.some((c) => c.name === editForm.carrier)" :value="editForm.carrier">
              {{ editForm.carrier }}
            </option>
          </select>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Tag</label>
            <input
              v-model="editForm.tag"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Group</label>
            <input
              v-model="editForm.group"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Number-specific webhook URL</label>
          <input
            v-model="editForm.webhook"
            placeholder="Optional"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
      </div>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="editing = null"
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

    <!-- Auto-reply modal -->
    <AppModal v-if="arNumber" :title="`Auto-reply — ${formatNumber(arNumber.number)}`" @close="arNumber = null">
      <div v-if="arLoading" class="text-sm text-slate-500">Loading…</div>
      <div v-else-if="arConfig" class="space-y-4">
        <div class="grid grid-cols-2 gap-2 rounded-md border border-slate-800 bg-slate-950 p-3 text-xs">
          <div class="text-slate-500">Master switch (env)</div>
          <div :class="arConfig.master_enabled ? 'text-emerald-400' : 'text-red-400'">
            {{ arConfig.master_enabled ? 'on' : 'off' }}
          </div>
          <div class="text-slate-500">Effective enabled</div>
          <div :class="arConfig.effective_enabled ? 'text-emerald-400' : 'text-slate-400'">
            {{ arConfig.effective_enabled ? 'yes' : 'no' }}
          </div>
          <div class="text-slate-500">Suppressed by STOP</div>
          <div class="text-slate-300">{{ arConfig.suppressed_by_stop ? 'yes' : 'no' }}</div>
          <div v-if="arConfig.env_default_fallback" class="text-slate-500">Env default message</div>
          <div v-if="arConfig.env_default_fallback" class="text-slate-300">{{ arConfig.env_default_fallback }}</div>
        </div>

        <label class="flex items-center gap-2 text-sm text-slate-300">
          <input v-model="arForm.enabled" type="checkbox" class="accent-indigo-500" />
          Enable auto-reply for this number
        </label>

        <div v-if="arForm.enabled" class="space-y-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">
              Reply message <span class="text-slate-600">(blank = use env fallback)</span>
            </label>
            <textarea
              v-model="arForm.message"
              rows="2"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Cooldown (seconds, 0 = none)</label>
            <input
              v-model.number="arForm.cooldown_secs"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
        </div>
      </div>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="arNumber = null"
        >
          Close
        </button>
        <button
          v-if="arConfig"
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="arSaving"
          @click="submitAutoReply"
        >
          {{ arSaving ? 'Saving…' : 'Save auto-reply' }}
        </button>
      </template>
    </AppModal>
    <!-- Bulk auto-reply modal -->
    <AppModal v-if="showBulkAr" :title="`Bulk auto-reply — ${selected.size} number(s)`" @close="showBulkAr = false">
      <div class="space-y-4">
        <label class="flex items-center gap-2 text-sm text-slate-300">
          <input v-model="bulkArForm.enabled" type="checkbox" class="accent-indigo-500" :disabled="bulkArRunning" />
          Enable auto-reply on selected numbers
        </label>
        <div v-if="bulkArForm.enabled" class="space-y-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">
              Reply message <span class="text-slate-600">(blank = use env fallback)</span>
            </label>
            <textarea
              v-model="bulkArForm.message"
              rows="2"
              :disabled="bulkArRunning"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Cooldown (seconds, 0 = none)</label>
            <input
              v-model.number="bulkArForm.cooldown_secs"
              type="number"
              min="0"
              :disabled="bulkArRunning"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
            />
          </div>
        </div>

        <div v-if="bulkArResults.length" class="max-h-48 overflow-y-auto rounded-md border border-slate-800">
          <div
            v-for="r in bulkArResults"
            :key="r.num"
            class="flex items-center justify-between border-b border-slate-800/50 px-3 py-1.5 text-xs"
          >
            <span class="font-mono text-slate-300">{{ formatNumber(r.num) }}</span>
            <span :class="r.ok ? 'text-emerald-400' : 'text-red-400'">
              {{ r.ok ? 'updated' : `failed — ${r.detail}` }}
            </span>
          </div>
        </div>
      </div>
      <template #footer>
        <button
          class="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          @click="showBulkAr = false"
        >
          Close
        </button>
        <button
          class="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          :disabled="bulkArRunning"
          @click="submitBulkAr"
        >
          {{ bulkArRunning ? 'Applying…' : `Apply to ${selected.size} number(s)` }}
        </button>
      </template>
    </AppModal>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getClientSettings, updateClientSettings } from '../../api'
import type { Client, ClientSettings, ClientSettingsUpdate } from '../../api/types'
import { useToastStore } from '../../stores/toast'
import { errorMessage } from '../../utils/format'

const props = defineProps<{ client: Client }>()
const emit = defineEmits<{ changed: [] }>()

const toasts = useToastStore()
const loading = ref(true)
const saving = ref(false)

const form = ref<ClientSettings>({
  auth_method: 'basic',
  api_format: 'generic',
  disable_message_splitting: false,
  webhook_retries: 3,
  webhook_timeout_secs: 10,
  include_raw_segments: false,
  default_webhook: '',
  sms_burst_limit: 0,
  sms_daily_limit: 0,
  sms_monthly_limit: 0,
  mms_burst_limit: 0,
  mms_daily_limit: 0,
  mms_monthly_limit: 0,
  limit_both: false,
})

onMounted(async () => {
  loading.value = true
  try {
    const s = await getClientSettings(props.client.id)
    if (s) form.value = { ...form.value, ...s }
  } catch (e) {
    // Settings may not exist yet — fall back to whatever came with the client object.
    if (props.client.settings) form.value = { ...form.value, ...props.client.settings }
    if (!(e instanceof Error && e.message.includes('404'))) {
      // non-fatal; defaults are shown
    }
  } finally {
    loading.value = false
  }
})

async function save() {
  saving.value = true
  try {
    const payload: ClientSettingsUpdate = { ...form.value }
    await updateClientSettings(props.client.id, payload)
    toasts.success('Settings updated.')
    emit('changed')
  } catch (e) {
    toasts.error(errorMessage(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div v-if="loading" class="text-sm text-slate-500">Loading…</div>
  <form v-else class="max-w-2xl space-y-6" @submit.prevent="save">
    <section class="rounded-lg border border-slate-800 bg-slate-900 p-4">
      <h3 class="mb-3 text-sm font-semibold text-slate-100">API &amp; auth</h3>
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">API format</label>
          <select
            v-model="form.api_format"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option value="generic">generic</option>
            <option value="bicom">bicom (PBXware)</option>
            <option value="telnyx">telnyx</option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Auth method</label>
          <select
            v-model="form.auth_method"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          >
            <option value="basic">basic</option>
            <option value="bearer">bearer</option>
          </select>
        </div>
      </div>
    </section>

    <section class="rounded-lg border border-slate-800 bg-slate-900 p-4">
      <h3 class="mb-3 text-sm font-semibold text-slate-100">Webhooks</h3>
      <div class="space-y-3">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">Default webhook URL</label>
          <input
            v-model="form.default_webhook"
            placeholder="https://…"
            class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
          />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Retries</label>
            <input
              v-model.number="form.webhook_retries"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-slate-400">Timeout (seconds)</label>
            <input
              v-model.number="form.webhook_timeout_secs"
              type="number"
              min="0"
              class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
            />
          </div>
        </div>
        <label class="flex items-center gap-2 text-sm text-slate-300">
          <input v-model="form.include_raw_segments" type="checkbox" class="accent-indigo-500" />
          Include raw segments in webhook payload
        </label>
        <label class="flex items-center gap-2 text-sm text-slate-300">
          <input v-model="form.disable_message_splitting" type="checkbox" class="accent-indigo-500" />
          Disable message splitting (web→web only)
        </label>
      </div>
    </section>

    <section class="rounded-lg border border-slate-800 bg-slate-900 p-4">
      <h3 class="mb-3 text-sm font-semibold text-slate-100">Usage limits <span class="font-normal text-slate-500">(0 = unlimited)</span></h3>
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">SMS burst</label>
          <input v-model.number="form.sms_burst_limit" type="number" min="0" class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">SMS daily</label>
          <input v-model.number="form.sms_daily_limit" type="number" min="0" class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">SMS monthly</label>
          <input v-model.number="form.sms_monthly_limit" type="number" min="0" class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">MMS burst</label>
          <input v-model.number="form.mms_burst_limit" type="number" min="0" class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">MMS daily</label>
          <input v-model.number="form.mms_daily_limit" type="number" min="0" class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-slate-400">MMS monthly</label>
          <input v-model.number="form.mms_monthly_limit" type="number" min="0" class="w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none" />
        </div>
      </div>
      <label class="mt-3 flex items-center gap-2 text-sm text-slate-300">
        <input v-model="form.limit_both" type="checkbox" class="accent-indigo-500" />
        Apply limits to inbound too
      </label>
    </section>

    <button
      type="submit"
      :disabled="saving"
      class="rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      {{ saving ? 'Saving…' : 'Save settings' }}
    </button>
  </form>
</template>

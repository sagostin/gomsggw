import { http } from './http'
import type {
  ApiKeyCreateRequest,
  ApiKeyCreateResponse,
  AutoReplyConfig,
  AutoReplyUpdate,
  Carrier,
  CarrierCreateRequest,
  Client,
  ClientCreateRequest,
  ClientSettings,
  ClientSettingsUpdate,
  ClientUpdateRequest,
  Failover,
  LegacyStatus,
  NumberUpdateRequest,
  StatsResponse,
  TenantApiKey,
} from './types'

// --- Stats ---
export const getStats = () => http.get<StatsResponse>('/stats')

// --- Carriers ---
export const listCarriers = () => http.get<Carrier[]>('/carriers')
export const createCarrier = (payload: CarrierCreateRequest) => http.post<Carrier>('/carriers', payload)
export const reloadCarriers = () => http.post<{ status?: string }>('/carriers/reload')

// --- Clients ---
export const listClients = () => http.get<Client[]>('/clients')
export const createClient = (payload: ClientCreateRequest) => http.post<Client>('/clients', payload)
export const updateClient = (id: number, payload: ClientUpdateRequest) =>
  http.patch<{ message: string; client: Client }>(`/clients/${id}`, payload)
export const deleteClient = (id: number) => http.delete<{ status?: string }>(`/clients/${id}`)
export const reloadClients = () => http.post<{ status?: string }>('/clients/reload')
export const changeClientPassword = (id: number, newPassword: string) =>
  http.patch<{ status?: string }>(`/clients/${id}/password`, { new_password: newPassword })

export const getClientSettings = (id: number) => http.get<ClientSettings>(`/clients/${id}/settings`)
export const updateClientSettings = (id: number, payload: ClientSettingsUpdate) =>
  http.put<{ message: string; settings: ClientSettings }>(`/clients/${id}/settings`, payload)

// --- Numbers ---
export const addNumber = (clientId: number, number: string, carrier: string, tag?: string, group?: string) =>
  http.post(`/clients/${clientId}/numbers`, { number, carrier, tag, group })
export const updateNumber = (clientId: number, numberId: number, payload: NumberUpdateRequest) =>
  http.put(`/clients/${clientId}/numbers/${numberId}`, payload)
export const deleteNumber = (clientId: number, numberId: number) =>
  http.delete(`/clients/${clientId}/numbers/${numberId}`)

// --- Auto-reply ---
export const getAutoReply = (numberId: number) => http.get<AutoReplyConfig>(`/numbers/${numberId}/auto-reply`)
export const updateAutoReply = (numberId: number, payload: AutoReplyUpdate) =>
  http.put(`/numbers/${numberId}/auto-reply`, payload)

// --- API Keys ---
export const listApiKeys = (clientId: number) => http.get<TenantApiKey[]>(`/clients/${clientId}/api-keys`)
export const createApiKey = (clientId: number, payload: ApiKeyCreateRequest) =>
  http.post<ApiKeyCreateResponse>(`/clients/${clientId}/api-keys`, payload)
export const revokeApiKey = (clientId: number, keyId: number) =>
  http.delete(`/clients/${clientId}/api-keys/${keyId}`)

// --- Failover ---
export const listFailovers = (clientId: number) => http.get<Failover[]>(`/clients/${clientId}/failovers`)
export const addFailover = (clientId: number, fallbackClientId: number, priority: number) =>
  http.post(`/clients/${clientId}/failovers`, { fallback_client_id: fallbackClientId, priority })
export const updateFailover = (clientId: number, failoverId: number, payload: { priority?: number; enabled?: boolean }) =>
  http.put(`/clients/${clientId}/failovers/${failoverId}`, payload)
export const removeFailover = (clientId: number, failoverId: number) =>
  http.delete(`/clients/${clientId}/failovers/${failoverId}`)

// --- Legacy (SMPP + MM4) status ---
export const getLegacyStatus = (clientId: number) => http.get<LegacyStatus>(`/clients/${clientId}/legacy-status`)

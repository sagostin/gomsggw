import { defineStore } from 'pinia'

const BASE_URL_KEY = 'msggw.baseUrl'
const API_KEY_KEY = 'msggw.apiKey'

function normalizeBaseUrl(url: string): string {
  return url.trim().replace(/\/+$/, '')
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    baseUrl: normalizeBaseUrl(sessionStorage.getItem(BASE_URL_KEY) || ''),
    apiKey: sessionStorage.getItem(API_KEY_KEY) || '',
  }),
  getters: {
    isConnected: (s) => s.apiKey.length > 0,
    authHeader: (s) => `Basic ${btoa(`apikey:${s.apiKey}`)}`,
    displayUrl: (s) => s.baseUrl || window.location.origin,
  },
  actions: {
    connect(baseUrl: string, apiKey: string) {
      this.baseUrl = normalizeBaseUrl(baseUrl)
      this.apiKey = apiKey.trim()
      sessionStorage.setItem(BASE_URL_KEY, this.baseUrl)
      sessionStorage.setItem(API_KEY_KEY, this.apiKey)
    },
    disconnect() {
      this.baseUrl = ''
      this.apiKey = ''
      sessionStorage.removeItem(BASE_URL_KEY)
      sessionStorage.removeItem(API_KEY_KEY)
    },
  },
})

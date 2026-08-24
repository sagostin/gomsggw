import { defineStore } from 'pinia'

export type ToastKind = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  kind: ToastKind
  message: string
}

let nextId = 1

export const useToastStore = defineStore('toasts', {
  state: () => ({ toasts: [] as Toast[] }),
  actions: {
    push(kind: ToastKind, message: string, timeoutMs = 4000) {
      const id = nextId++
      this.toasts.push({ id, kind, message })
      setTimeout(() => this.dismiss(id), timeoutMs)
    },
    success(message: string) {
      this.push('success', message)
    },
    error(message: string) {
      this.push('error', message, 6000)
    },
    info(message: string) {
      this.push('info', message)
    },
    dismiss(id: number) {
      this.toasts = this.toasts.filter((t) => t.id !== id)
    },
  },
})

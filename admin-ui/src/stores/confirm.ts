import { defineStore } from 'pinia'

interface ConfirmOptions {
  title: string
  message: string
  confirmLabel?: string
  danger?: boolean
}

interface ConfirmState extends ConfirmOptions {
  resolve: (ok: boolean) => void
}

export const useConfirmStore = defineStore('confirm', {
  state: () => ({ current: null as ConfirmState | null }),
  actions: {
    ask(options: ConfirmOptions): Promise<boolean> {
      return new Promise((resolve) => {
        this.current = { ...options, resolve }
      })
    },
    answer(ok: boolean) {
      this.current?.resolve(ok)
      this.current = null
    },
  },
})

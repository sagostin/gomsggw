export function formatDate(value?: string | null): string {
  if (!value) return 'never'
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  return d.toLocaleString()
}

export function formatLimit(n?: number): string {
  if (!n || n <= 0) return 'unlimited'
  return n.toLocaleString()
}

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

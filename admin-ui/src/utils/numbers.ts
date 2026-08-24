// Number parsing/normalization ported from scripts/main.py parse_numbers_csv.

export interface ParsedNumbers {
  valid: string[]
  invalid: string[]
  /** entries that were rewritten during normalization, e.g. "+1-250-555-1234" → "12505551234" */
  rewritten: { from: string; to: string }[]
}

export function normalizeNumber(num: string): string {
  return num.replace(/[^\d]/g, '')
}

/**
 * Parse comma- or newline-separated numbers into NANP 11-digit form.
 * Accepts 10-digit (prefixes "1"), 11-digit, and E.164-ish inputs.
 */
export function parseNumbersCsv(raw: string): ParsedNumbers {
  const parts = raw
    .replace(/\n/g, ',')
    .split(',')
    .map((p) => p.trim())
    .filter(Boolean)

  const valid: string[] = []
  const invalid: string[] = []
  const rewritten: { from: string; to: string }[] = []

  for (const p of parts) {
    let normalized = normalizeNumber(p)
    if (normalized.length >= 10 && normalized.length <= 15) {
      if (normalized.length === 10) normalized = '1' + normalized
      else if (normalized.length > 11) normalized = normalized.slice(-11)
      valid.push(normalized)
      if (normalized !== normalizeNumber(p)) rewritten.push({ from: p, to: normalized })
    } else {
      invalid.push(p)
    }
  }
  return { valid, invalid, rewritten }
}

/** Pretty-print an 11-digit NANP number as +1 (XXX) XXX-XXXX. */
export function formatNumber(num: string): string {
  if (/^1\d{10}$/.test(num)) {
    return `+1 (${num.slice(1, 4)}) ${num.slice(4, 7)}-${num.slice(7)}`
  }
  return num
}

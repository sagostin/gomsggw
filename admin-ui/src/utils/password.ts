const LOWER = 'abcdefghijklmnopqrstuvwxyz'
const UPPER = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'
const DIGITS = '0123456789'
const ALPHABET = LOWER + UPPER + DIGITS

function randomInt(max: number): number {
  const array = new Uint32Array(1)
  crypto.getRandomValues(array)
  return array[0]! % max
}

/** Generate a strong random password (mirrors scripts/main.py generate_password). */
export function generatePassword(length = 24): string {
  for (;;) {
    let pwd = ''
    for (let i = 0; i < length; i++) pwd += ALPHABET[randomInt(ALPHABET.length)]
    if (/[a-z]/.test(pwd) && /[A-Z]/.test(pwd) && /[0-9]/.test(pwd)) return pwd
  }
}

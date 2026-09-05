import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// shadcn's class joiner, kept next to the copied components (not in lib/:
// lib/ is domain rules with tests, this is a styling helper).
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

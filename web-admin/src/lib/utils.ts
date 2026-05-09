import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function asArray<T>(val: T | T[] | null | undefined): T[] {
  if (val === null || val === undefined) return [];
  if (Array.isArray(val)) return val;
  if (typeof val === 'string' && (val as string).startsWith('[') && (val as string).endsWith(']')) {
    try {
      return JSON.parse(val as string);
    } catch (e) {
      return [val as T];
    }
  }
  return [val];
}

export function safeJson<T>(val: any, fallback: T): T {
  if (typeof val !== 'string') return val ?? fallback;
  try {
    return JSON.parse(val);
  } catch (e) {
    return fallback;
  }
}

export function withToken(url: string): string {
  if (!url) return '';
  // Only add token to local /api paths
  if (!url.startsWith('/api') && !url.startsWith('http://localhost') && !url.startsWith('http://127.0.0.1')) {
    return url;
  }
  const token = 'ciao';
  const separator = url.includes('?') ? '&' : '?';
  return `${url}${separator}token=${token}`;
}

export function formatDate(date: string | number | Date): string {
  if (!date) return '-';
  try {
    return new Date(date).toLocaleDateString('it-IT', {
      day: '2-digit',
      month: '2-digit',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  } catch (e) {
    return String(date);
  }
}

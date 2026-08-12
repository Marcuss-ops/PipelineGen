export function toStringArray(value) {
  if (value == null || value === '') return [];
  if (typeof value === 'string') return value.split(',').map((s) => s.trim()).filter(Boolean);
  if (Array.isArray(value)) {
    return value
      .filter((v) => v != null && v !== '')
      .map((v) => (typeof v === 'string' ? v.trim() : String(v).trim()))
      .filter(Boolean);
  }
  return [];
}

export function toString(value) {
  if (value == null) return '';
  return String(value).trim();
}

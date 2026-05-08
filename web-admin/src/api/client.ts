const API_BASE = import.meta.env.VITE_API_BASE_URL || '';
const API_TOKEN = import.meta.env.VITE_API_TOKEN || 'ciao';

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${API_TOKEN}`,
      ...(init?.headers || {}),
    },
  });

  if (!response.ok) {
    const text = await response.text().catch(() => '');
    throw new Error(text || `HTTP ${response.status}`);
  }

  return response.json() as Promise<T>;
}

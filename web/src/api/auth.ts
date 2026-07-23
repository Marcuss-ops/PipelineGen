import { request } from './http'

export interface AuthResponse {
  ok: boolean
}

export function login(token: string): Promise<AuthResponse> {
  return request<AuthResponse>('/admin/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
}

export function logout(): Promise<AuthResponse> {
  return request<AuthResponse>('/admin/auth/logout', {
    method: 'POST',
  })
}

export function checkAuth(): Promise<AuthResponse> {
  return request<AuthResponse>('/admin/auth/me')
}

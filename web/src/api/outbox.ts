import { request } from './http'

export interface OutboxStatusResponse {
  ok: boolean
  counts: Record<string, number>
}

export interface OutboxEventsResponse {
  ok: boolean
  events: any[]
  count: number
}

export function getOutboxStatus(): Promise<OutboxStatusResponse> {
  return request<OutboxStatusResponse>('/assets/operator/outbox/status')
}

export function getOutboxEvents(status?: string): Promise<OutboxEventsResponse> {
  const query = status ? `?status=${encodeURIComponent(status)}` : ''
  return request<OutboxEventsResponse>(`/assets/operator/outbox/events${query}`)
}

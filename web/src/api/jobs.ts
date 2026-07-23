import { request } from './http'

export interface JobSummary {
  id: string
  type: string
  status: string
  progress: number
  correlation_id?: string
  created_at?: string
  updated_at?: string
  error?: string
}

export interface JobEvent {
  id?: string
  type: string
  message?: string
  data?: Record<string, unknown>
  created_at?: string
}

export interface JobFull {
  id: string
  type: string
  status: string
  correlation_id?: string
  current_stage?: string
  current_step?: string
  progress: number
  error?: string
  result?: Record<string, unknown>
  created_at?: string
  started_at?: string
  updated_at?: string
  timeline?: JobEvent[]
  events?: JobEvent[]
  retryable?: boolean
  job?: Record<string, unknown>
}

export interface JobsListResponse {
  jobs: JobSummary[]
  count: number
}

export function listJobs(
  filters: {
    status?: string
    type?: string
    correlation_id?: string
    limit?: number
    offset?: number
  } = {}
): Promise<JobsListResponse> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.type) params.set('type', filters.type)
  if (filters.correlation_id) params.set('correlation_id', filters.correlation_id)
  if (filters.limit !== undefined) params.set('limit', String(filters.limit))
  if (filters.offset !== undefined) params.set('offset', String(filters.offset))
  const query = params.toString()
  return request<JobsListResponse>(`/jobs${query ? `?${query}` : ''}`)
}

export function getJobFull(id: string): Promise<JobFull> {
  return request<JobFull>(`/jobs/${encodeURIComponent(id)}/full`)
}

export function getJobEvents(id: string): Promise<{ events: JobEvent[]; count: number }> {
  return request<{ events: JobEvent[]; count: number }>(`/jobs/${encodeURIComponent(id)}/events`)
}

export function cancelJob(id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export function retryJob(id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/jobs/${encodeURIComponent(id)}/retry`, { method: 'POST' })
}

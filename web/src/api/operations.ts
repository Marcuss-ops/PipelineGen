import { request } from './http'

export interface AdminFieldDescriptor {
  key: string
  label: string
  type: string
  editable?: boolean
  required?: boolean
  filterable?: boolean
  sortable?: boolean
  options?: string[]
  description?: string
}

export interface AdminActionDescriptor {
  key: string
  label: string
  description?: string
  dangerous?: boolean
}

export interface AdminEntitySchema {
  entity: string
  label: string
  primary_key: string
  readable: boolean
  editable: boolean
  bulk_editable: boolean
  fields: AdminFieldDescriptor[]
  actions: AdminActionDescriptor[]
}

export interface AdminListResponse {
  items: Record<string, unknown>[]
  total: number
  limit: number
  offset: number
}

export function listAdminEntities(): Promise<AdminEntitySchema[]> {
  return request<AdminEntitySchema[]>('/admin/entities')
}

export function getAdminEntitySchema(entity: string): Promise<AdminEntitySchema> {
  return request<AdminEntitySchema>(`/admin/entities/${encodeURIComponent(entity)}/schema`)
}

export function listAdminEntityRecords(entity: string, params: Record<string, string> = {}): Promise<AdminListResponse> {
  const searchParams = new URLSearchParams(params)
  const query = searchParams.toString()
  return request<AdminListResponse>(`/admin/entities/${encodeURIComponent(entity)}${query ? `?${query}` : ''}`)
}

export function getAdminEntityRecord(entity: string, id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/admin/entities/${encodeURIComponent(entity)}/${encodeURIComponent(id)}`)
}

export function patchAdminEntityRecord(
  entity: string,
  id: string,
  changes: Record<string, unknown>,
  expectedVersion = 0
): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/admin/entities/${encodeURIComponent(entity)}/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ expected_version: expectedVersion, changes }),
  })
}

export function runAdminEntityAction(
  entity: string,
  id: string,
  action: string,
  payload: Record<string, unknown> = {}
): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(
    `/admin/entities/${encodeURIComponent(entity)}/${encodeURIComponent(id)}/actions/${encodeURIComponent(action)}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }
  )
}

export function getOperationsErrors(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/assets/operator/operations/errors')
}

export function getHealth(deep = false): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/health${deep ? '?deep=true' : ''}`)
}

export function getReady(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/ready')
}

export function getModels(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/models')
}

export function getQdrantReady(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/qdrant/ready')
}

export function getMediaIndexHealth(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/media/index-health')
}

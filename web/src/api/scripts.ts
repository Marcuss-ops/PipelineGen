import { request } from './http'

export interface ScriptScene {
  id: string
  index: number
  text: Record<string, string>
  clip?: Record<string, unknown>
  voiceover?: Record<string, { id: string; url?: string; duration?: number }>
}

export interface ScriptDocument {
  id: string
  link: string
}

export interface ScriptRenderJob {
  job_id: string
  status: string
}

export interface ScriptJobFull {
  ok: boolean
  job_id: string
  job?: { id: string; type: string; status: string }
  status: string
  error?: string
  result?: Record<string, unknown>
  current_stage: string
  stages: Record<string, string>
  scenes?: ScriptScene[]
  documents?: Record<string, ScriptDocument>
  render_job?: ScriptRenderJob
  word_count?: number
  error_code?: string
  error_message?: string
  failed_stage?: string
  attempt_count?: number
  next_retry_at?: string
}

export function getScriptJobFull(id: string): Promise<ScriptJobFull> {
  return request<ScriptJobFull>(`/script/jobs/${encodeURIComponent(id)}/full`)
}

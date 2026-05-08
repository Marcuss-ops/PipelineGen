import { apiFetch } from './client';

export type JobStatus = 'pending' | 'queued' | 'processing' | 'running' | 'completed' | 'failed' | 'paused' | 'cancelled' | 'zombie' | 'retrying';

export type JobType = 'media.artlist' | 'media.youtube_clip' | 'content.package' | 'workflow.run' | 'media.extract' | string;

export interface Job {
  id: string;
  type: JobType;
  status: JobStatus;
  priority: number;
  project?: string;
  video_name?: string;
  active_key?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  cancelled_at?: string;
  worker_id?: string;
  payload?: Record<string, unknown>;
  result?: Record<string, unknown>;
  error?: string;
  retry_count: number;
  max_retries: number;
  progress: number;
}

export interface JobEvent {
  id: string;
  job_id: string;
  type: string;
  message: string;
  data_json?: string;
  created_at: string;
}

export interface ListJobsResponse {
  ok: boolean;
  jobs: Job[];
  total: number;
}

export async function listJobs(params?: {
  status?: JobStatus;
  type?: JobType;
  limit?: number;
  offset?: number;
}): Promise<ListJobsResponse> {
  const query = new URLSearchParams();
  if (params?.status) query.set('status', params.status);
  if (params?.type) query.set('type', params.type);
  if (params?.limit) query.set('limit', String(params.limit));
  if (params?.offset) query.set('offset', String(params.offset));

  const queryString = query.toString();
  return apiFetch(`/api/jobs${queryString ? '?' + queryString : ''}`);
}

export async function getJob(id: string): Promise<{ ok: boolean; job: Job }> {
  return apiFetch(`/api/jobs/${id}`);
}

export async function getJobEvents(id: string): Promise<{ ok: boolean; events: JobEvent[] }> {
  return apiFetch(`/api/jobs/${id}/events`);
}

export async function cancelJob(id: string): Promise<{ ok: boolean }> {
  return apiFetch(`/api/jobs/${id}/cancel`, { method: 'POST' });
}

export async function retryJob(id: string): Promise<{ ok: boolean }> {
  return apiFetch(`/api/jobs/${id}/retry`, { method: 'POST' });
}

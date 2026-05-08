import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, Clock, Loader2, Pause, RefreshCw, XCircle } from 'lucide-react';
import { useState } from 'react';
import { cancelJob, listJobs, retryJob } from '../api/jobs';
import type { Job, JobStatus, JobType } from '../api/jobs';
import { Button } from '../components/ui/Button';

const STATUS_COLORS: Record<JobStatus, string> = {
  pending: 'bg-zinc-100 text-zinc-700',
  queued: 'bg-blue-100 text-blue-700',
  processing: 'bg-yellow-100 text-yellow-700',
  running: 'bg-yellow-100 text-yellow-700',
  completed: 'bg-emerald-100 text-emerald-700',
  failed: 'bg-red-100 text-red-700',
  paused: 'bg-zinc-100 text-zinc-700',
  cancelled: 'bg-zinc-100 text-zinc-700',
  zombie: 'bg-orange-100 text-orange-700',
  retrying: 'bg-yellow-100 text-yellow-700',
};

const STATUS_ICONS: Record<JobStatus, typeof CheckCircle2> = {
  pending: Clock,
  queued: Clock,
  processing: Loader2,
  running: Loader2,
  completed: CheckCircle2,
  failed: XCircle,
  paused: Pause,
  cancelled: XCircle,
  zombie: AlertTriangle,
  retrying: RefreshCw,
};

export function JobsPage() {
  const [statusFilter, setStatusFilter] = useState<JobStatus | 'all'>('all');
  const [typeFilter, setTypeFilter] = useState<JobType | 'all'>('all');

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['jobs', statusFilter, typeFilter],
    queryFn: () => listJobs({
      status: statusFilter === 'all' ? undefined : statusFilter,
      type: typeFilter === 'all' ? undefined : typeFilter,
      limit: 100,
    }),
  });

  const jobs = data?.jobs ?? [];

  const handleCancel = async (id: string) => {
    await cancelJob(id);
    refetch();
  };

  const handleRetry = async (id: string) => {
    await retryJob(id);
    refetch();
  };

  return (
    <div className="space-y-6">
      <section className="rounded-3xl border border-zinc-200 bg-white p-8 shadow-sm">
        <h2 className="text-3xl font-extrabold tracking-tight">Job Queue Dashboard</h2>
        <p className="mt-2 text-sm text-zinc-500">Monitor and manage background jobs</p>
      </section>

      <section className="rounded-3xl border border-zinc-200 bg-white p-6 shadow-sm">
        <div className="flex flex-wrap gap-2 mb-6">
          <Button variant={statusFilter === 'all' ? 'primary' : 'secondary'} onClick={() => setStatusFilter('all')}>All</Button>
          <Button variant={statusFilter === 'running' ? 'primary' : 'secondary'} onClick={() => setStatusFilter('running')}>Running</Button>
          <Button variant={statusFilter === 'failed' ? 'primary' : 'secondary'} onClick={() => setStatusFilter('failed')}>Failed</Button>
          <Button variant={statusFilter === 'completed' ? 'primary' : 'secondary'} onClick={() => setStatusFilter('completed')}>Completed</Button>
          <Button variant={statusFilter === 'pending' ? 'primary' : 'secondary'} onClick={() => setStatusFilter('pending')}>Pending</Button>
        </div>

        {isLoading ? (
          <div className="py-16 text-center"><Loader2 className="h-8 w-8 animate-spin text-zinc-300 mx-auto" /></div>
        ) : jobs.length === 0 ? (
          <div className="py-16 text-center text-sm text-zinc-500">No jobs found</div>
        ) : (
          <div className="space-y-3">
            {jobs.map((job) => {
              const StatusIcon = STATUS_ICONS[job.status] || Clock;
              const isRunning = job.status === 'running' || job.status === 'processing';
              const isFailed = job.status === 'failed';
              const isTerminal = job.status === 'completed' || job.status === 'cancelled';

              return (
                <div key={job.id} className="rounded-2xl border border-zinc-200 p-4 transition hover:bg-zinc-50">
                  <div className="flex items-start justify-between">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <StatusIcon className={`h-4 w-4 ${isRunning ? 'animate-spin' : ''}`} />
                        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_COLORS[job.status] || 'bg-zinc-100'}`}>
                          {job.status}
                        </span>
                        <span className="text-sm font-semibold text-zinc-900">{job.type}</span>
                      </div>
                      <div className="mt-1 text-xs text-zinc-500">
                        {job.id}
                        {job.video_name && <span className="ml-2">• {job.video_name}</span>}
                      </div>
                      {job.error && (
                        <div className="mt-2 rounded-lg bg-red-50 p-2 text-xs text-red-700">{job.error}</div>
                      )}
                    </div>
                    <div className="flex gap-2">
                      {isFailed && (
                        <Button variant="secondary" size="sm" onClick={() => handleRetry(job.id)}>
                          <RefreshCw className="h-3.5 w-3.5" /> Retry
                        </Button>
                      )}
                      {!isTerminal && (
                        <Button variant="danger" size="sm" onClick={() => handleCancel(job.id)}>
                          <XCircle className="h-3.5 w-3.5" /> Cancel
                        </Button>
                      )}
                    </div>
                  </div>

                  {isRunning && job.progress > 0 && (
                    <div className="mt-3">
                      <div className="h-2 overflow-hidden rounded-full bg-zinc-100">
                        <div
                          className="h-full bg-blue-500 transition-all duration-300"
                          style={{ width: `${Math.min(job.progress, 100)}%` }}
                        />
                      </div>
                      <p className="mt-1 text-xs text-zinc-500">Progress: {job.progress}%</p>
                    </div>
                  )}

                  <div className="mt-3 flex gap-4 text-xs text-zinc-500">
                    <span>Created: {new Date(job.created_at).toLocaleString()}</span>
                    {job.started_at && <span>Started: {new Date(job.started_at).toLocaleString()}</span>}
                    {job.completed_at && <span>Completed: {new Date(job.completed_at).toLocaleString()}</span>}
                    <span>Retries: {job.retry_count}/{job.max_retries}</span>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}

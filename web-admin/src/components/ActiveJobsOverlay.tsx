import React, { useEffect, useState } from 'react';
import { listJobs, type Job } from '../api/jobs';
import { JobProgressBar } from './JobProgressBar';
import { Loader2, ChevronUp, ChevronDown, CheckCircle2, AlertCircle } from 'lucide-react';

export const ActiveJobsOverlay: React.FC = () => {
  const [activeJobs, setActiveJobs] = useState<Job[]>([]);
  const [isExpanded, setIsExpanded] = useState(false);
  const [lastUpdate, setLastUpdate] = useState<Date>(new Date());

  useEffect(() => {
    const fetchJobs = async () => {
      try {
        const response = await listJobs({ 
          limit: 10,
          // We don't filter by status to show recently completed/failed too
        });
        
        // Filter for jobs that are actually "active" or recently finished
        const interesting = response.jobs.filter(j => 
          ['running', 'processing', 'queued', 'pending'].includes(j.status) ||
          (new Date().getTime() - new Date(j.updated_at).getTime() < 30000) // completed in last 30s
        );
        
        setActiveJobs(interesting);
        setLastUpdate(new Date());
      } catch (err) {
        console.error('Failed to fetch active jobs:', err);
      }
    };

    fetchJobs();
    const interval = setInterval(fetchJobs, 3000);
    return () => clearInterval(interval);
  }, []);

  if (activeJobs.length === 0) return null;

  const runningCount = activeJobs.filter(j => ['running', 'processing'].includes(j.status)).length;

  return (
    <div className="fixed bottom-4 right-4 z-50 w-80 bg-white/90 backdrop-blur-md border border-gray-200 rounded-xl shadow-2xl overflow-hidden transition-all duration-300">
      <div 
        className="px-4 py-3 bg-gray-50 flex items-center justify-between cursor-pointer hover:bg-gray-100 transition-colors"
        onClick={() => setIsExpanded(!isExpanded)}
      >
        <div className="flex items-center gap-2">
          {runningCount > 0 ? (
            <Loader2 className="w-4 h-4 text-blue-500 animate-spin" />
          ) : (
            <CheckCircle2 className="w-4 h-4 text-green-500" />
          )}
          <span className="text-sm font-semibold text-gray-800">
            {runningCount > 0 ? `${runningCount} Jobs in Progress` : 'All Jobs Complete'}
          </span>
        </div>
        <button className="text-gray-500 hover:text-gray-700">
          {isExpanded ? <ChevronDown className="w-4 h-4" /> : <ChevronUp className="w-4 h-4" />}
        </button>
      </div>

      {isExpanded && (
        <div className="max-h-80 overflow-y-auto p-4 space-y-4 bg-white/50">
          {activeJobs.map(job => (
            <div key={job.id} className="p-2 rounded-lg border border-gray-100 bg-white/80 shadow-sm">
              <JobProgressBar job={job} />
            </div>
          ))}
          <div className="text-[10px] text-gray-400 text-center pt-2">
            Last update: {lastUpdate.toLocaleTimeString()}
          </div>
        </div>
      )}
    </div>
  );
};

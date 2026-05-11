import React from 'react';
import type { Job } from '../api/jobs';

interface JobProgressBarProps {
  job: Job;
}

export const JobProgressBar: React.FC<JobProgressBarProps> = ({ job }) => {
  const getStatusColor = () => {
    switch (job.status) {
      case 'completed': return 'bg-green-500';
      case 'failed': return 'bg-red-500';
      case 'processing':
      case 'running': return 'bg-blue-500';
      case 'pending':
      case 'queued': return 'bg-yellow-500';
      default: return 'bg-gray-500';
    }
  };

  return (
    <div className="w-full">
      <div className="flex justify-between items-center mb-1 text-xs">
        <span className="font-medium text-gray-700 truncate max-w-[200px]">
          {job.type}
        </span>
        <span className="text-gray-500">{job.progress}%</span>
      </div>
      <div className="w-full bg-gray-200 rounded-full h-1.5 overflow-hidden">
        <div
          className={`h-full transition-all duration-500 ${getStatusColor()}`}
          style={{ width: `${job.progress}%` }}
        />
      </div>
      {job.error && (
        <p className="mt-1 text-[10px] text-red-500 truncate">{job.error}</p>
      )}
    </div>
  );
};

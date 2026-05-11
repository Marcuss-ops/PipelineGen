import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { getDiagnostics } from '../api/media';
import { Server, Database, Cloud, Cpu, Activity, CheckCircle2, XCircle, AlertCircle } from 'lucide-react';

export const DiagnosticsPage: React.FC = () => {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['diagnostics'],
    queryFn: getDiagnostics,
    refetchInterval: 10000,
  });

  if (isLoading) {
    return (
      <div className="flex flex-col items-center justify-center min-h-[400px] gap-4">
        <Activity className="w-8 h-8 text-blue-500 animate-pulse" />
        <p className="text-gray-500 animate-pulse">Running system diagnostics...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-6 bg-red-50 border border-red-200 rounded-xl flex items-start gap-4">
        <AlertCircle className="w-6 h-6 text-red-500 mt-0.5" />
        <div>
          <h3 className="font-bold text-red-800">Diagnostics Failed</h3>
          <p className="text-red-600">{(error as Error).message}</p>
          <button 
            onClick={() => refetch()}
            className="mt-3 px-4 py-2 bg-red-100 text-red-700 rounded-lg hover:bg-red-200 transition-colors text-sm font-medium"
          >
            Retry Diagnostics
          </button>
        </div>
      </div>
    );
  }

  const services = data?.services || {};
  const repos = data?.repositories || {};
  const env = data?.environment || {};
  const drive = data?.drive || {};

  const StatusIcon = ({ status }: { status: boolean | string }) => {
    if (status === true || status === 'connected') return <CheckCircle2 className="w-5 h-5 text-green-500" />;
    return <XCircle className="w-5 h-5 text-red-500" />;
  };

  return (
    <div className="space-y-6">
      <div className="flex justify-between items-end mb-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">System Diagnostics</h1>
          <p className="text-gray-500">Real-time health status of PipelineGen services and dependencies.</p>
        </div>
        <button 
          onClick={() => refetch()}
          className="text-xs font-medium text-blue-600 hover:text-blue-700 bg-blue-50 px-3 py-1.5 rounded-full transition-colors"
        >
          Force Refresh
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Core Services */}
        <div className="bg-white p-5 rounded-2xl border border-gray-100 shadow-sm space-y-4">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-blue-50 rounded-lg">
              <Server className="w-5 h-5 text-blue-600" />
            </div>
            <h3 className="font-bold text-gray-800">Core Services</h3>
          </div>
          <div className="space-y-3">
            {Object.entries(services).map(([name, status]) => (
              <div key={name} className="flex items-center justify-between">
                <span className="text-sm text-gray-600 capitalize">{name}</span>
                <StatusIcon status={status as boolean} />
              </div>
            ))}
          </div>
        </div>

        {/* Repositories */}
        <div className="bg-white p-5 rounded-2xl border border-gray-100 shadow-sm space-y-4">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-purple-50 rounded-lg">
              <Database className="w-5 h-5 text-purple-600" />
            </div>
            <h3 className="font-bold text-gray-800">Databases</h3>
          </div>
          <div className="space-y-3">
            {Object.entries(repos).map(([name, status]) => (
              <div key={name} className="flex items-center justify-between">
                <span className="text-sm text-gray-600 capitalize">{name}</span>
                <StatusIcon status={status as string} />
              </div>
            ))}
          </div>
        </div>

        {/* Cloud & Storage */}
        <div className="bg-white p-5 rounded-2xl border border-gray-100 shadow-sm space-y-4">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-orange-50 rounded-lg">
              <Cloud className="w-5 h-5 text-orange-600" />
            </div>
            <h3 className="font-bold text-gray-800">Cloud Sync</h3>
          </div>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm text-gray-600">Google Drive</span>
              <StatusIcon status={drive.status} />
            </div>
          </div>
        </div>

        {/* Environment */}
        <div className="bg-white p-5 rounded-2xl border border-gray-100 shadow-sm space-y-4">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-green-50 rounded-lg">
              <Cpu className="w-5 h-5 text-green-600" />
            </div>
            <h3 className="font-bold text-gray-800">Environment</h3>
          </div>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm text-gray-600">Go Version</span>
              <span className="text-xs font-mono bg-gray-100 px-2 py-0.5 rounded text-gray-700">{env.go_version}</span>
            </div>
          </div>
        </div>
      </div>

      {/* Raw JSON Debug */}
      <div className="bg-gray-900 rounded-2xl overflow-hidden shadow-xl">
        <div className="px-4 py-2 bg-gray-800 flex justify-between items-center border-b border-gray-700">
          <span className="text-xs font-bold text-gray-400 uppercase tracking-wider">Raw Diagnostics Payload</span>
          <div className="flex gap-1.5">
            <div className="w-2.5 h-2.5 rounded-full bg-red-500" />
            <div className="w-2.5 h-2.5 rounded-full bg-yellow-500" />
            <div className="w-2.5 h-2.5 rounded-full bg-green-500" />
          </div>
        </div>
        <pre className="p-6 overflow-x-auto text-xs text-blue-300 font-mono leading-relaxed">
          {JSON.stringify(data, null, 2)}
        </pre>
      </div>
    </div>
  );
};

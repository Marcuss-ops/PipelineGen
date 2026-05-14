import { Plus, Search, Trash2, RefreshCw, ShieldAlert, Activity, Video } from 'lucide-react';
import { MediaSource } from '../lib/types';
import { SourceTabs } from '../components/SourceTabs';
import { Button } from '../components/ui/Button';
import { SOURCES, sourceById } from '../lib/sources';
import { cn } from '../lib/utils';

type FilterType = 'all' | 'processed' | 'missingDrive' | 'withErrors';

interface MediaToolbarProps {
  source: MediaSource;
  search: string;
  isLiveSearch: boolean;
  activeFilter: FilterType;
  allSourceCounts: Record<string, number>;
  onSourceChange: (source: MediaSource) => void;
  onSearchChange: (search: string) => void;
  onLiveSearchToggle: () => void;
  onFilterChange: (filter: FilterType) => void;
  onCleanupOrphans: () => void;
  onSyncImages: () => void;
  onAddAsset: () => void;
  onLiveSearchExecute: () => void;
  syncMutation: { isPending: boolean };
}

export function MediaToolbar({
  source,
  search,
  isLiveSearch,
  activeFilter,
  allSourceCounts,
  onSourceChange,
  onSearchChange,
  onLiveSearchToggle,
  onFilterChange,
  onCleanupOrphans,
  onSyncImages,
  onAddAsset,
  onLiveSearchExecute,
  syncMutation,
}: MediaToolbarProps) {
  return (
    <section className="rounded-3xl border border-zinc-200 bg-white p-8 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
      <SourceTabs 
        active={source} 
        counts={allSourceCounts} 
        onChange={(next) => { 
          onSourceChange(next); 
          onFilterChange('all'); 
        }} 
      />
      <div className="mt-5 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h3 className="text-lg font-bold text-zinc-900 dark:text-zinc-100">{sourceById[source].label}</h3>
          <p className="text-sm text-zinc-500 dark:text-zinc-400">{sourceById[source].description}</p>
        </div>
        <div className="relative w-full md:max-w-xl flex gap-2">
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400 dark:text-zinc-500" />
            <input 
              value={search} 
              onChange={(event) => onSearchChange(event.target.value)} 
              onKeyDown={(e) => {
                if (e.key === 'Enter' && isLiveSearch) {
                  onLiveSearchExecute();
                }
              }}
              placeholder={isLiveSearch ? "Cerca ovunque (es: chuck norris -15)..." : `Cerca in ${sourceById[source].label}...`} 
              className="h-10 w-full rounded-xl border border-zinc-200 bg-zinc-50 pl-9 pr-3 text-sm outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200 dark:focus:border-blue-500 dark:focus:bg-zinc-900" 
            />
          </div>
          <Button 
            variant={isLiveSearch ? 'primary' : 'secondary'} 
            onClick={() => {
              if (isLiveSearch && search) {
                onLiveSearchExecute();
              } else {
                onLiveSearchToggle();
              }
            }}
            className="whitespace-nowrap"
          >
            <Activity className={cn("h-4 w-4 mr-2", isLiveSearch && "animate-pulse")} />
            {isLiveSearch ? 'Cerca Live' : 'Live Mode'}
          </Button>
        </div>
      </div>
    </section>
  );
}

interface MediaAdminHeaderProps {
  source: MediaSource;
  onCleanupOrphans: () => void;
  onSyncImages: () => void;
  onAddAsset: () => void;
  syncMutation: { isPending: boolean };
}

export function MediaAdminHeader({ 
  source, 
  onCleanupOrphans, 
  onSyncImages, 
  onAddAsset,
  syncMutation 
}: MediaAdminHeaderProps) {
  return (
    <div className="flex items-center justify-between">
      <h1 className="text-2xl font-black tracking-tight text-zinc-900">Media Database</h1>
      <div className="flex gap-2">
        <Button variant="secondary" onClick={onCleanupOrphans}>
          <ShieldAlert className="h-4 w-4" /> Pulisci Orfani
        </Button>
        {source === 'images' && (
          <Button variant="secondary" onClick={onSyncImages} disabled={syncMutation.isPending}>
            <RefreshCw className={`h-4 w-4 ${syncMutation.isPending ? 'animate-spin' : ''}`} /> Sincronizza Drive
          </Button>
        )}
        <Button onClick={onAddAsset}>
          <Plus className="h-4 w-4" /> Aggiungi asset
        </Button>
      </div>
    </div>
  );
}

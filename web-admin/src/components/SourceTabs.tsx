import type { MediaSource } from '../lib/types';
import { SOURCES } from '../lib/sources';
import { cn } from '../lib/utils';

export function SourceTabs({ active, counts, onChange }: { active: MediaSource; counts: Record<string, number>; onChange: (source: MediaSource) => void }) {
  return (
    <div className="overflow-x-auto scrollbar-hide border-b border-zinc-200">
      <div className="flex min-w-max gap-6">
        {SOURCES.map((source) => {
          const isActive = source.id === active;
          return (
            <button
              key={source.id}
              onClick={() => onChange(source.id)}
              className={cn('relative flex h-14 items-center gap-2 border-b-2 text-sm font-semibold transition', isActive ? 'border-zinc-950 text-zinc-950' : 'border-transparent text-zinc-500 hover:text-zinc-900')}
            >
              <span className={cn('h-2 w-2 rounded-full', source.accent)} />
              {source.label}
              <span className={cn('rounded-full px-2 py-0.5 text-xs', isActive ? 'bg-zinc-950 text-white' : 'bg-zinc-100 text-zinc-500')}>{counts[source.id] ?? 0}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

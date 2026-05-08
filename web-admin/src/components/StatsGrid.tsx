import { AlertTriangle, CheckCircle2, Cloud, Database, Loader2 } from 'lucide-react';
import type { MediaItem } from '../lib/types';

export type FilterType = 'all' | 'processed' | 'missingDrive' | 'missingHash';

export function StatsGrid({
  items,
  isLoading,
  activeFilter,
  onFilter,
}: {
  items: MediaItem[];
  isLoading?: boolean;
  activeFilter?: FilterType;
  onFilter?: (filter: FilterType) => void;
}) {
  const processed = items.filter((item) => item.drive_link || item.download_link).length;
  const missingDrive = items.filter((item) => !item.drive_link && !item.download_link).length;
  const missingHash = items.filter((item) => !item.file_hash).length;
  const cards: { label: string; value: number; icon: React.ElementType; filter: FilterType }[] = [
    { label: 'Totale record', value: items.length, icon: Database, filter: 'all' },
    { label: 'Processati', value: processed, icon: CheckCircle2, filter: 'processed' },
    { label: 'Senza Drive', value: missingDrive, icon: Cloud, filter: 'missingDrive' },
    { label: 'Senza hash', value: missingHash, icon: AlertTriangle, filter: 'missingHash' },
  ];
  return (
    <div className="grid gap-6 sm:grid-cols-2 xl:grid-cols-4">
      {cards.map((card) => {
        const isActive = activeFilter === card.filter;
        return (
          <button
            key={card.label}
            onClick={() => onFilter?.(card.filter)}
            className={`rounded-3xl border p-8 text-left shadow-sm transition hover:shadow-md ${
              isActive ? 'border-blue-500 bg-blue-50' : 'border-zinc-200 bg-white'
            }`}
          >
            <div className="flex items-center justify-between">
              <span className={`text-sm font-medium ${isActive ? 'text-blue-700' : 'text-zinc-500'}`}>{card.label}</span>
              <card.icon className={`h-6 w-6 ${isActive ? 'text-blue-500' : 'text-zinc-400'}`} />
            </div>
            <div className={`mt-4 text-4xl font-extrabold tracking-tight ${isActive ? 'text-blue-900' : 'text-zinc-900'}`}>
              {isLoading ? (
                <Loader2 className="h-8 w-8 animate-spin text-zinc-300" />
              ) : (
                card.value
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}

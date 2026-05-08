import { AlertTriangle, CheckCircle2, Cloud, Database } from 'lucide-react';
import type { MediaItem } from '../lib/types';

export function StatsGrid({ items }: { items: MediaItem[] }) {
  const processed = items.filter((item) => item.drive_link || item.download_link).length;
  const missingDrive = items.filter((item) => !item.drive_link && !item.download_link).length;
  const missingHash = items.filter((item) => !item.file_hash).length;
  const cards = [
    { label: 'Totale record', value: items.length, icon: Database },
    { label: 'Processati', value: processed, icon: CheckCircle2 },
    { label: 'Senza Drive', value: missingDrive, icon: Cloud },
    { label: 'Senza hash', value: missingHash, icon: AlertTriangle },
  ];
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {cards.map((card) => (
        <div key={card.label} className="rounded-2xl border border-zinc-200 bg-white p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-zinc-500">{card.label}</span>
            <card.icon className="h-4 w-4 text-zinc-400" />
          </div>
          <div className="mt-2 text-2xl font-bold tracking-tight">{card.value}</div>
        </div>
      ))}
    </div>
  );
}

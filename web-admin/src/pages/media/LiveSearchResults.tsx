import { Video } from 'lucide-react';
import { Button } from '../components/ui/Button';
import { apiFetch } from '../api/client';

interface LiveSearchResultsProps {
  results: any;
  onNotice: (msg: string) => void;
}

export function LiveSearchResults({ results, onNotice }: LiveSearchResultsProps) {
  if (!results.youtube || results.youtube.results.length === 0) return null;

  return (
    <div className="space-y-6">
      <div className="bg-white rounded-3xl border border-zinc-200 p-6">
        <div className="flex items-center gap-2 mb-4">
          <Video className="w-5 h-5 text-red-600" />
          <h2 className="text-lg font-bold">Risultati YouTube</h2>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {results.youtube.results.map((item: any) => (
            <div key={item.id} className="group relative bg-zinc-50 rounded-2xl overflow-hidden border border-zinc-100 hover:shadow-lg transition-all">
              <img src={item.thumb_url || item.thumbnail} alt={item.name} className="w-full aspect-video object-cover" />
              <div className="p-3">
                <h4 className="text-sm font-semibold truncate">{item.name}</h4>
                <p className="text-[10px] text-zinc-500 mt-1">{item.duration}s</p>
                <Button 
                  size="sm" 
                  className="w-full mt-2" 
                  onClick={() => {
                    onNotice(`Avviata estrazione per ${item.name}`);
                    apiFetch('/api/assets/youtube/clips', {
                      method: 'POST',
                      body: JSON.stringify({ 
                        video_url: item.external_url || `https://youtube.com/watch?v=${item.id}`,
                        name: item.name
                      })
                    }).catch(err => onNotice(`Errore: ${err}`));
                  }}
                >
                  Estrai Clip
                </Button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

export const SOURCES = [
  { id: 'artlist', label: 'Artlist', description: 'Artlist assets' },
  { id: 'youtube', label: 'YouTube', description: 'YouTube clips' },
  { id: 'stock', label: 'Stock Footage', description: 'Raw clips from stock folders' },
  { id: 'images', label: 'Images', description: 'Image assets from Drive' },
  { id: 'voiceover', label: 'Voiceover', description: 'Generated voiceovers' }
];

export const sourceById = SOURCES.reduce((acc, s) => {
  acc[s.id] = s;
  return acc;
}, {} as Record<string, any>);

export type MediaSource = 'artlist' | 'youtube' | 'stock' | 'images' | 'voiceover';

export const SOURCES = [
  { id: 'artlist', label: 'Artlist', description: 'Artlist assets', accent: 'bg-yellow-400' },
  { id: 'youtube', label: 'YouTube', description: 'YouTube clips', accent: 'bg-red-500' },
  { id: 'stock', label: 'Stock Footage', description: 'Raw clips from stock folders', accent: 'bg-blue-400' },
  { id: 'images', label: 'Images', description: 'Image assets from Drive', accent: 'bg-green-400' },
  { id: 'voiceover', label: 'Voiceover', description: 'Generated voiceovers', accent: 'bg-purple-400' }
];

export const sourceById = SOURCES.reduce((acc, s) => {
  acc[s.id] = s;
  return acc;
}, {} as Record<string, any>);

export type MediaSource = 'artlist' | 'youtube' | 'stock' | 'images' | 'voiceover';

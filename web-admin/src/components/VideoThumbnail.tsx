import { useState, useRef, useEffect } from 'react';
import type { MediaItem } from '../lib/types';

function getDriveFileId(value?: string) {
  if (!value) return '';
  const fileMatch = value.match(/\/file\/d\/([^/?]+)/);
  if (fileMatch?.[1]) return fileMatch[1];
  const dMatch = value.match(/\/d\/([^/?]+)/);
  if (dMatch?.[1]) return dMatch[1];
  const idMatch = value.match(/[?&]id=([^&]+)/);
  if (idMatch?.[1]) return idMatch[1];
  return '';
}

export function VideoThumbnail({ item, className = "" }: { item: MediaItem, className?: string }) {
  const [isHovering, setIsHovering] = useState(false);
  const [error, setError] = useState(false);
  const videoRef = useRef<HTMLVideoElement>(null);

  const driveFileId = item.drive_file_id || getDriveFileId(item.drive_link) || getDriveFileId(item.download_link);
  
  // Basic extension check
  const isBadExtension = (filename?: string) => {
    if (!filename) return false;
    const ext = filename.toLowerCase().split('.').pop();
    return ['txt', 'json', 'md', 'html', 'pdf', 'csv'].includes(ext || '');
  };

  // The download endpoint is now our smart proxy (Local -> Drive)
  const hasProxyVideo = !!(item.local_path || driveFileId) && !isBadExtension(item.filename || item.name);
  const proxySrc = hasProxyVideo 
    ? `/api/media/${item.source}/clips/${item.id}/download` 
    : (item.preview_url || item.download_link);

  const posterSrc = item.thumb_url || (driveFileId ? `https://drive.google.com/thumbnail?id=${driveFileId}&sz=w400` : `https://placehold.co/112x112?text=${encodeURIComponent(item.source)}`);

  useEffect(() => {
    if (isHovering && !error && videoRef.current) {
      videoRef.current.play().catch(() => {
        // Auto-play might be blocked
      });
    } else if (videoRef.current) {
      videoRef.current.pause();
      videoRef.current.currentTime = 0;
    }
  }, [isHovering, error]);

  return (
    <div 
      className={`relative overflow-hidden bg-zinc-100 ring-2 ring-zinc-900/5 ${className}`}
      onMouseEnter={() => setIsHovering(true)}
      onMouseLeave={() => setIsHovering(false)}
    >
      {/* Thumbnail Image */}
      <img 
        src={posterSrc} 
        className={`h-full w-full object-cover transition-opacity duration-300 ${isHovering && !error && hasProxyVideo ? 'opacity-0' : 'opacity-100'}`}
        alt="" 
      />

      {/* Video Preview */}
      {isHovering && proxySrc && !error && (
        <video
          ref={videoRef}
          src={proxySrc}
          muted
          loop
          autoPlay
          playsInline
          onError={() => setError(true)}
          className="absolute inset-0 h-full w-full object-cover"
        />
      )}
    </div>
  );
}

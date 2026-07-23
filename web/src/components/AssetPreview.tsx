import { getAssetPreviewUrl } from '../api/assets'

interface AssetPreviewProps {
  id: string
  mediaType?: string
  thumbnailUrl?: string
  name?: string
  size?: number
}

export default function AssetPreview({ id, mediaType, thumbnailUrl, name, size = 120 }: AssetPreviewProps) {
  const previewUrl = getAssetPreviewUrl(id)

  if (thumbnailUrl) {
    return (
      <img
        src={thumbnailUrl}
        alt={name || 'preview'}
        style={{ width: size, height: size, objectFit: 'cover', borderRadius: '6px' }}
      />
    )
  }

  if (mediaType?.startsWith('audio')) {
    return (
      <div
        style={{
          width: size,
          height: size,
          borderRadius: '6px',
          background: 'linear-gradient(135deg, #1e293b, #0f172a)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: '1px solid #334155',
        }}
      >
        <span style={{ fontSize: '2rem' }}>🔊</span>
      </div>
    )
  }

  if (mediaType?.startsWith('video') || mediaType === 'clip') {
    return (
      <video
        src={previewUrl}
        preload="metadata"
        style={{ width: size, height: size, objectFit: 'cover', borderRadius: '6px' }}
      />
    )
  }

  if (mediaType?.startsWith('image')) {
    return (
      <img
        src={previewUrl}
        alt={name || 'preview'}
        style={{ width: size, height: size, objectFit: 'cover', borderRadius: '6px' }}
      />
    )
  }

  return (
    <div
      style={{
        width: size,
        height: size,
        borderRadius: '6px',
        background: 'linear-gradient(135deg, #1e293b, #0f172a)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        border: '1px solid #334155',
        color: '#94a3b8',
        fontSize: '0.75rem',
        textAlign: 'center',
        padding: '0.5rem',
      }}
    >
      {mediaType || 'Asset'}
    </div>
  )
}

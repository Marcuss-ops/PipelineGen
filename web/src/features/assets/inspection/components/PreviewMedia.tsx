import styles from '../AssetInspector.module.css'

interface PreviewMediaProps {
  url: string
  mediaType?: string
}

export function PreviewMedia({ url, mediaType }: PreviewMediaProps) {
  const lower = (mediaType || '').toLowerCase()

  if (lower.startsWith('video') || lower === 'clip') {
    return <video src={url} controls autoPlay className={styles.mediaPreview} />
  }

  if (lower.startsWith('audio')) {
    return <audio src={url} controls autoPlay className={styles.mediaPreview} />
  }

  return (
    <img
      src={url}
      alt="preview"
      className={styles.mediaPreview}
      onError={(e) => {
        ;(e.target as HTMLImageElement).style.display = 'none'
      }}
    />
  )
}

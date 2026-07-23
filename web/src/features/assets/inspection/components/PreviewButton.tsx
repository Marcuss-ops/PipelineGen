import { useState } from 'react'
import { getAssetPreviewUrl } from '../../../../api/client'
import styles from '../AssetInspector.module.css'
import { PreviewMedia } from './PreviewMedia'

interface PreviewButtonProps {
  id: string
  mediaType?: string
}

export function PreviewButton({ id, mediaType }: PreviewButtonProps) {
  const [open, setOpen] = useState(false)
  const url = getAssetPreviewUrl(id)

  return (
    <>
      <button onClick={() => setOpen(true)} className={styles.primaryButton}>
        Anteprima file
      </button>
      {open && (
        <div className={styles.modalOverlay} onClick={() => setOpen(false)}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <PreviewMedia url={url} mediaType={mediaType} />
            <button onClick={() => setOpen(false)} className={styles.modalClose}>
              ✕
            </button>
          </div>
        </div>
      )}
    </>
  )
}

import { AssetStatusData } from '../../../api/assets'

export interface AssetStatusBadgeProps {
  status?: AssetStatusData | string | null
}

export function AssetStatusBadge({ status }: AssetStatusBadgeProps) {
  if (!status) return null

  let code: string
  let label: string
  let severity: string

  if (typeof status === 'string') {
    code = status
    label = status
    severity = 'info'
  } else {
    code = status.code
    label = status.label || status.code
    severity = status.severity || 'info'
  }

  if (code === 'UNKNOWN') {
    label = `UNKNOWN: ${label}`
  }

  let bg = 'rgba(148,163,184,0.1)'
  let color = '#94a3b8'
  switch (severity) {
    case 'success':
      bg = 'rgba(74,222,128,0.1)'
      color = '#4ade80'
      break
    case 'info':
      bg = 'rgba(56,189,248,0.1)'
      color = '#38bdf8'
      break
    case 'warning':
      bg = 'rgba(250,204,21,0.1)'
      color = '#facc15'
      break
    case 'error':
      bg = 'rgba(248,113,113,0.1)'
      color = '#f87171'
      break
    case 'neutral':
    default:
      bg = 'rgba(148,163,184,0.1)'
      color = '#94a3b8'
  }

  return (
    <span
      style={{
        display: 'inline-block',
        background: bg,
        color: color,
        padding: '0.2rem 0.5rem',
        borderRadius: '9999px',
        fontSize: '0.75rem',
        fontWeight: 500,
        whiteSpace: 'nowrap',
      }}
      title={(() => {
        const hints: string[] = [code]
        if (typeof status === 'object') {
          if (status.terminal) hints.push('terminale')
          if (status.retryable) hints.push('retryable')
        }
        return hints.join(' • ')
      })()}
    >
      {label}
    </span>
  )
}

export function Badge({ text }: { text: string }) {
  return (
    <span
      style={{
        display: 'inline-block',
        background: 'rgba(56,189,248,0.1)',
        color: '#38bdf8',
        padding: '0.2rem 0.5rem',
        borderRadius: '9999px',
        fontSize: '0.75rem',
        fontWeight: 500,
        whiteSpace: 'nowrap',
      }}
    >
      {text}
    </span>
  )
}

export function formatDate(value: string | undefined) {
  if (!value) return '-'
  try {
    return new Date(value).toLocaleDateString('it-IT')
  } catch {
    return value
  }
}

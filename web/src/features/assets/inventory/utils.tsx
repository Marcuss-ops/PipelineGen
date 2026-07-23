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

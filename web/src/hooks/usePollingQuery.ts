import { useCallback, useEffect, useRef, useState } from 'react'

export interface UsePollingQueryOptions<T> {
  queryFn: () => Promise<T>
  interval: number
  enabled?: boolean
  pause?: boolean
  onError?: (err: unknown) => void
}

export interface UsePollingQueryResult<T> {
  data: T | null
  loading: boolean
  error: string | null
  refresh: () => void
}

/**
 * Shared polling hook for the admin dashboard.
 *
 * Runs `queryFn` immediately on mount (or when enabled/pause change),
 * then re-runs it every `interval` milliseconds while `enabled` is true
 * and `pause` is false. Cleans up its single interval on unmount or
 * when polling should stop — there is no scattered `setInterval` logic.
 *
 * `refresh()` triggers an immediate manual fetch without waiting for the
 * next interval tick. The query function reference is kept fresh via a
 * ref, so callers do not need to memoize it.
 */
export function usePollingQuery<T>({
  queryFn,
  interval,
  enabled = true,
  pause = false,
  onError,
}: UsePollingQueryOptions<T>): UsePollingQueryResult<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const queryFnRef = useRef(queryFn)
  const onErrorRef = useRef(onError)
  const didInitRef = useRef(false)
  const isMountedRef = useRef(true)

  useEffect(() => {
    queryFnRef.current = queryFn
    onErrorRef.current = onError
  })

  const tick = useCallback(async () => {
    try {
      const result = await queryFnRef.current()
      if (!isMountedRef.current) return
      setData(result)
      setError(null)
    } catch (err) {
      if (!isMountedRef.current) return
      const message = err instanceof Error ? err.message : 'Errore sconosciuto'
      setError(message)
      onErrorRef.current?.(err)
    } finally {
      if (isMountedRef.current) {
        setLoading(false)
      }
    }
  }, [])

  const refresh = useCallback(() => {
    setLoading(true)
    setError(null)
    tick()
  }, [tick])

  useEffect(() => {
    isMountedRef.current = true
    let id: ReturnType<typeof setInterval> | null = null

    const runner = async () => {
      if (!isMountedRef.current) return
      await tick()
    }

    if (!enabled) {
      setLoading(false)
      didInitRef.current = false
      return () => {
        isMountedRef.current = false
      }
    }

    // Run the initial fetch once, even when paused, so the caller
    // can decide from fresh data whether polling should continue.
    if (!didInitRef.current) {
      didInitRef.current = true
      setLoading(true)
      runner()
    }

    if (!pause) {
      id = setInterval(runner, interval)
    }

    return () => {
      isMountedRef.current = false
      if (id) clearInterval(id)
    }
  }, [interval, enabled, pause, tick])

  return { data, loading, error, refresh }
}

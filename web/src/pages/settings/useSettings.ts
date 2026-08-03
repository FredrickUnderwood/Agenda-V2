import { useQuery } from '@tanstack/react-query'
import * as api from '@/api/settings'
import type { Setting } from '@/api/types'

export const SETTINGS_QUERY_KEY = ['settings'] as const

// Shared loader for the whole Settings page. Every section reads from this one
// cached query (react-query dedupes by key), and every mutation invalidates
// SETTINGS_QUERY_KEY, so the page stays consistent without prop-drilling.
export function useSettings() {
  const query = useQuery({ queryKey: SETTINGS_QUERY_KEY, queryFn: api.listSettings })
  const list = query.data?.data ?? []
  const byKey = new Map<string, Setting>(list.map((s) => [s.key, s]))
  return { ...query, list, byKey }
}

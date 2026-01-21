import useSWR, { useSWRConfig } from 'swr'
import { useState, useEffect } from 'react'

const INFRA_ADMIN_ROLE = 'admin'

export function useUser() {
  const { cache } = useSWRConfig()
  const { data: user, error, mutate } = useSWR('/api/users/self')
  const { data: org } = useSWR(() => (user ? '/api/organizations/self' : null))
  const { data: { items: grants } = {}, grantsError } = useSWR(() =>
    user
      ? `/api/grants?user=${user?.id}&showInherited=1&resource=infra&limit=1000`
      : null
  )

  return {
    user,
    loading: !user && !error,
    org,
    isAdmin: grants?.some(g => g.privilege === INFRA_ADMIN_ROLE),
    isAdminLoading: !!user && !grants && !grantsError,
    login: async body => {
      const res = await fetch('/api/login', {
        method: 'POST',
        body: JSON.stringify(body),
      })

      const data = await jsonBody(res)

      // Fetch user data after successful login to ensure the cookie is set
      // and user data is available before navigation
      try {
        const userRes = await fetch('/api/users/self')
        const userData = await jsonBody(userRes)
        
        // Update the SWR cache with the fetched user data
        if (userData) {
          await mutate(userData, false)
        } else {
          await mutate()
        }
      } catch (error) {
        // If fetching user data fails, fall back to triggering revalidation
        // This maintains backward compatibility if /api/users/self fails
        await mutate()
      }

      return data
    },
    logout: async () => {
      await fetch('/api/logout', { method: 'POST' })

      // Set user to undefined without revalidation first
      // This prevents SWR from trying to refetch user data
      await mutate(undefined, false)
      
      // Clear the entire cache to remove org, grants, and other user-specific data
      // Note: This is safe to do after mutate because mutate already updated the SWR internal state
      cache.clear()
    },
  }
}

export function useDebouncedSearch(searchTerm, delay = 300) {
  const [debouncedTerm, setDebouncedTerm] = useState('')

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedTerm(searchTerm), delay)
    return () => clearTimeout(timer)
  }, [searchTerm, delay])

  return debouncedTerm
}

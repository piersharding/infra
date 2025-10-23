import { useState, useEffect, useCallback } from 'react'
import { useRouter } from 'next/router'

/**
 * Custom hook for handling search functionality with manual search execution
 *
 * @param {Object} options - Configuration options
 * @param {string} options.searchParam - URL parameter name for search (default: 'search')
 * @param {string} options.pageParam - URL parameter name for page (default: 'p')
 * @param {boolean} options.resetPageOnSearch - Whether to reset to page 1 on search (default: true)
 * @param {boolean} options.updateUrl - Whether to update URL with search params (default: true)
 * @returns {Object} Search state and handlers
 */
export function useSearch({
    searchParam = 'search',
    pageParam = 'p',
    resetPageOnSearch = true,
    updateUrl = true,
} = {}) {
    const router = useRouter()

    // Search state
    const [searchQuery, setSearchQuery] = useState(() => {
        // Initialize from URL on mount
        const urlSearch = router.query[searchParam]
        return typeof urlSearch === 'string' ? urlSearch : ''
    })
    const [activeSearchQuery, setActiveSearchQuery] = useState(() => {
        // Initialize from URL on mount
        const urlSearch = router.query[searchParam]
        return typeof urlSearch === 'string' ? urlSearch : ''
    })

    // Sync with URL changes only when URL actually changes
    // This prevents interference while typing
    useEffect(() => {
        const urlSearch = router.query[searchParam]
        const urlSearchValue = typeof urlSearch === 'string' ? urlSearch : ''
        
        // Only update if URL search differs from our active search
        // This prevents the effect from running while user is typing
        if (urlSearchValue !== activeSearchQuery) {
            setSearchQuery(urlSearchValue)
            setActiveSearchQuery(urlSearchValue)
        }
    }, [router.query[searchParam], searchParam, activeSearchQuery])

    // Execute search manually
    const executeSearch = useCallback(() => {
        setActiveSearchQuery(searchQuery)
    }, [searchQuery])

    // Update URL when active search changes
    useEffect(() => {
        if (!updateUrl) return

        const currentPage = Math.max(parseInt(router.query[pageParam]) || 1, 1)
        const currentSearch = router.query[searchParam] || ''

        // Only update URL if the active search has actually changed from what's in the URL
        if (activeSearchQuery.trim() === currentSearch.trim()) {
            return
        }

        // Reset to first page when search changes and we're not already on page 1
        if (resetPageOnSearch && currentPage !== 1 && activeSearchQuery.trim()) {
            router.push({
                    pathname: router.pathname,
                    query: {
                        ...router.query,
                        [pageParam]: 1,
                        [searchParam]: activeSearchQuery.trim() || undefined,
                    },
                },
                undefined, { shallow: true }
            )
        } else {
            // Update URL with search parameter
            const newQuery = {...router.query }
            if (activeSearchQuery.trim()) {
                newQuery[searchParam] = activeSearchQuery.trim()
            } else {
                delete newQuery[searchParam]
            }

            router.push({
                    pathname: router.pathname,
                    query: newQuery,
                },
                undefined, { shallow: true }
            )
        }
    }, [
        activeSearchQuery,
        searchParam,
        pageParam,
        resetPageOnSearch,
        updateUrl
    ])

    // Build query parameters for API calls
    const buildSearchParams = useCallback((additionalParams = {}) => {
        const params = new URLSearchParams()

        // Add search parameter if present
        if (activeSearchQuery.trim()) {
            params.append('name', activeSearchQuery.trim())
        }

        // Add additional parameters
        Object.entries(additionalParams).forEach(([key, value]) => {
            if (value !== undefined && value !== null && value !== '') {
                params.append(key, value.toString())
            }
        })

        return params.toString()
    }, [activeSearchQuery])

    // Build full API URL with search parameters
    const buildApiUrl = useCallback((baseUrl, additionalParams = {}) => {
        const searchParams = buildSearchParams(additionalParams)
        return searchParams ? `${baseUrl}?${searchParams}` : baseUrl
    }, [buildSearchParams])

    // Clear search
    const clearSearch = useCallback(() => {
        setSearchQuery('')
        setActiveSearchQuery('')
    }, [])

    // Check if actively searching
    const hasActiveSearch = Boolean(activeSearchQuery.trim())

    // Generate appropriate messages
    const getEmptyMessage = useCallback((defaultMessage = 'No items') => {
        return hasActiveSearch ?
            `No results found for "${activeSearchQuery}"` :
            defaultMessage
    }, [hasActiveSearch, activeSearchQuery])

    const getResultMessage = useCallback((totalCount = 0, itemName = 'item') => {
        const itemText = totalCount === 1 ? itemName : `${itemName}s`
        return hasActiveSearch ?
            `Found ${totalCount} ${itemText} matching "${activeSearchQuery}"` :
            `Showing ${totalCount} ${itemText}`
    }, [hasActiveSearch, activeSearchQuery])

    return {
        // State
        searchQuery,
        activeSearchQuery,
        hasActiveSearch,

        // Actions
        setSearchQuery,
        executeSearch,
        clearSearch,

        // Utilities
        buildSearchParams,
        buildApiUrl,
        getEmptyMessage,
        getResultMessage,
    }
}

export default useSearch
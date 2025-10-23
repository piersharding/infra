import { renderHook, act, waitFor } from '@testing-library/react'
import { useRouter } from 'next/router'
import { useSearch } from '../../lib/useSearch'

// Mock Next.js router
jest.mock('next/router', () => ({
  useRouter: jest.fn(),
}))

describe('useSearch Hook', () => {
  const mockPush = jest.fn()
  const mockRouter = {
    query: {},
    pathname: '/test',
    push: mockPush,
  }

  beforeEach(() => {
    jest.clearAllMocks()
    jest.useFakeTimers()
    useRouter.mockReturnValue(mockRouter)
  })

  afterEach(() => {
    jest.runOnlyPendingTimers()
    jest.useRealTimers()
  })

  it('initializes with default values', () => {
    const { result } = renderHook(() => useSearch())

    expect(result.current.searchQuery).toBe('')
    expect(result.current.debouncedSearchQuery).toBe('')
    expect(result.current.isSearching).toBe(false)
    expect(result.current.hasActiveSearch).toBe(false)
  })

  it('initializes search query from URL', () => {
    useRouter.mockReturnValue({
      ...mockRouter,
      query: { search: 'test query' },
    })

    const { result } = renderHook(() => useSearch())

    expect(result.current.searchQuery).toBe('test query')
  })

  it('debounces search query updates', async () => {
    const { result } = renderHook(() => useSearch({ debounceMs: 300 }))

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.searchQuery).toBe('test')
    expect(result.current.debouncedSearchQuery).toBe('')
    expect(result.current.isSearching).toBe(true)

    // Fast-forward time by 300ms
    act(() => {
      jest.advanceTimersByTime(300)
    })

    await waitFor(() => {
      expect(result.current.debouncedSearchQuery).toBe('test')
      expect(result.current.isSearching).toBe(false)
    })
  })

  it('updates isSearching state correctly during debounce', () => {
    const { result } = renderHook(() => useSearch({ debounceMs: 300 }))

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.isSearching).toBe(true)

    act(() => {
      jest.advanceTimersByTime(300)
    })

    expect(result.current.isSearching).toBe(false)
  })

  it('clears search correctly', () => {
    const { result } = renderHook(() => useSearch())

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.searchQuery).toBe('test')

    act(() => {
      result.current.clearSearch()
    })

    expect(result.current.searchQuery).toBe('')
  })

  it('detects active search correctly', () => {
    const { result } = renderHook(() => useSearch())

    expect(result.current.hasActiveSearch).toBe(false)

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.hasActiveSearch).toBe(true)

    act(() => {
      result.current.setSearchQuery('   ')
    })

    expect(result.current.hasActiveSearch).toBe(false)
  })

  it('builds search parameters correctly', async () => {
    const { result } = renderHook(() => useSearch())

    act(() => {
      result.current.setSearchQuery('test query')
    })

    act(() => {
      jest.advanceTimersByTime(300)
    })

    await waitFor(() => {
      const params = result.current.buildSearchParams({ page: '1', limit: '50' })
      expect(params).toBe('name=test+query&page=1&limit=50')
    })
  })

  it('builds API URL correctly', async () => {
    const { result } = renderHook(() => useSearch())

    act(() => {
      result.current.setSearchQuery('test')
    })

    act(() => {
      jest.advanceTimersByTime(300)
    })

    await waitFor(() => {
      const url = result.current.buildApiUrl('/api/groups', { page: '1' })
      expect(url).toBe('/api/groups?name=test&page=1')
    })
  })

  it('builds API URL without search when query is empty', () => {
    const { result } = renderHook(() => useSearch())

    const url = result.current.buildApiUrl('/api/groups', { page: '1' })
    expect(url).toBe('/api/groups?page=1')
  })

  it('generates correct empty messages', () => {
    const { result } = renderHook(() => useSearch())

    expect(result.current.getEmptyMessage('No items')).toBe('No items')

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.getEmptyMessage('No items')).toBe('No results found for "test"')
  })

  it('generates correct result messages', () => {
    const { result } = renderHook(() => useSearch())

    expect(result.current.getResultMessage(5, 'group')).toBe('Showing 5 groups')
    expect(result.current.getResultMessage(1, 'group')).toBe('Showing 1 group')

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.getResultMessage(3, 'user')).toBe('Found 3 users matching "test"')
    expect(result.current.getResultMessage(1, 'user')).toBe('Found 1 user matching "test"')
  })

  describe('URL Management', () => {
    it('updates URL when search query changes', async () => {
      const { result } = renderHook(() => useSearch())

      act(() => {
        result.current.setSearchQuery('test')
      })

      act(() => {
        jest.advanceTimersByTime(300)
      })

      await waitFor(() => {
        expect(mockPush).toHaveBeenCalledWith(
          {
            pathname: '/test',
            query: { search: 'test' },
          },
          undefined,
          { shallow: true }
        )
      })
    })

    it('removes search parameter from URL when query is cleared', async () => {
      useRouter.mockReturnValue({
        ...mockRouter,
        query: { search: 'test', other: 'param' },
      })

      const { result } = renderHook(() => useSearch())

      act(() => {
        result.current.setSearchQuery('')
      })

      act(() => {
        jest.advanceTimersByTime(300)
      })

      await waitFor(() => {
        expect(mockPush).toHaveBeenCalledWith(
          {
            pathname: '/test',
            query: { other: 'param' },
          },
          undefined,
          { shallow: true }
        )
      })
    })

    it('resets to page 1 when search changes', async () => {
      useRouter.mockReturnValue({
        ...mockRouter,
        query: { p: '3' },
      })

      const { result } = renderHook(() => useSearch())

      act(() => {
        result.current.setSearchQuery('test')
      })

      act(() => {
        jest.advanceTimersByTime(300)
      })

      await waitFor(() => {
        expect(mockPush).toHaveBeenCalledWith(
          {
            pathname: '/test',
            query: { p: 1, search: 'test' },
          },
          undefined,
          { shallow: true }
        )
      })
    })

    it('does not reset page when resetPageOnSearch is false', async () => {
      useRouter.mockReturnValue({
        ...mockRouter,
        query: { p: '3' },
      })

      const { result } = renderHook(() => useSearch({ resetPageOnSearch: false }))

      act(() => {
        result.current.setSearchQuery('test')
      })

      act(() => {
        jest.advanceTimersByTime(300)
      })

      await waitFor(() => {
        expect(mockPush).toHaveBeenCalledWith(
          {
            pathname: '/test',
            query: { p: '3', search: 'test' },
          },
          undefined,
          { shallow: true }
        )
      })
    })

    it('does not update URL when updateUrl is false', async () => {
      const { result } = renderHook(() => useSearch({ updateUrl: false }))

      act(() => {
        result.current.setSearchQuery('test')
      })

      act(() => {
        jest.advanceTimersByTime(300)
      })

      // Wait a bit to ensure no URL update occurs
      await act(async () => {
        await new Promise(resolve => setTimeout(resolve, 50))
      })

      expect(mockPush).not.toHaveBeenCalled()
    })

    it('uses custom parameter names', async () => {
      const { result } = renderHook(() =>
        useSearch({
          searchParam: 'query',
          pageParam: 'page'
        })
      )

      act(() => {
        result.current.setSearchQuery('test')
      })

      act(() => {
        jest.advanceTimersByTime(300)
      })

      await waitFor(() => {
        expect(mockPush).toHaveBeenCalledWith(
          {
            pathname: '/test',
            query: { query: 'test' },
          },
          undefined,
          { shallow: true }
        )
      })
    })
  })

  describe('Edge Cases', () => {
    it('handles rapid search query changes', async () => {
      const { result } = renderHook(() => useSearch({ debounceMs: 300 }))

      act(() => {
        result.current.setSearchQuery('a')
      })

      act(() => {
        jest.advanceTimersByTime(100)
        result.current.setSearchQuery('ab')
      })

      act(() => {
        jest.advanceTimersByTime(100)
        result.current.setSearchQuery('abc')
      })

      // Only the last query should be debounced
      act(() => {
        jest.advanceTimersByTime(300)
      })

      await waitFor(() => {
        expect(result.current.debouncedSearchQuery).toBe('abc')
      })
    })

    it('handles whitespace-only search queries', () => {
      const { result } = renderHook(() => useSearch())

      act(() => {
        result.current.setSearchQuery('   ')
      })

      expect(result.current.hasActiveSearch).toBe(false)
      expect(result.current.getEmptyMessage('No items')).toBe('No items')
    })

    it('builds search parameters with undefined/null values', () => {
      const { result } = renderHook(() => useSearch())

      const params = result.current.buildSearchParams({
        page: '1',
        limit: null,
        other: undefined,
        empty: '',
      })

      expect(params).toBe('page=1')
    })
  })
})

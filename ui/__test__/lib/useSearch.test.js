import { renderHook, act } from '@testing-library/react'
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
    useRouter.mockReturnValue({
      ...mockRouter,
      query: { ...mockRouter.query },
    })
  })

  it('initializes with default values', () => {
    const { result } = renderHook(() => useSearch())

    expect(result.current.searchQuery).toBe('')
    expect(result.current.activeSearchQuery).toBe('')
    expect(result.current.hasActiveSearch).toBe(false)
  })

  it('initializes search query from URL', () => {
    useRouter.mockReturnValue({
      ...mockRouter,
      query: { search: 'test query' },
    })

    const { result } = renderHook(() => useSearch())

    expect(result.current.searchQuery).toBe('test query')
    expect(result.current.activeSearchQuery).toBe('test query')
    expect(result.current.hasActiveSearch).toBe(true)
  })

  it('does not update activeSearchQuery until the search is executed externally', () => {
    const { result } = renderHook(() => useSearch())

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.searchQuery).toBe('test')
    expect(result.current.activeSearchQuery).toBe('')
    expect(result.current.hasActiveSearch).toBe(false)
  })

  it('clears search correctly', () => {
    const { result } = renderHook(() => useSearch())

    act(() => {
      result.current.setSearchQuery('test')
    })

    expect(result.current.searchQuery).toBe('test')
    expect(result.current.activeSearchQuery).toBe('')
    expect(result.current.hasActiveSearch).toBe(false)

    act(() => {
      result.current.clearSearch()
    })

    expect(result.current.searchQuery).toBe('')
    expect(result.current.activeSearchQuery).toBe('')
    expect(result.current.hasActiveSearch).toBe(false)
  })

  it('builds API URL without search when active search is empty', () => {
    const { result } = renderHook(() => useSearch())

    const url = result.current.buildApiUrl('/api/groups', { page: '1' })
    expect(url).toBe('/api/groups?page=1')
  })

  it('generates default messages when not actively searching', () => {
    const { result } = renderHook(() => useSearch())

    expect(result.current.getEmptyMessage('No items')).toBe('No items')
    expect(result.current.getResultMessage(5, 'group')).toBe('Showing 5 groups')
    expect(result.current.getResultMessage(1, 'group')).toBe('Showing 1 group')
  })

  describe('URL Management', () => {
    it('does not update URL while typing (until activeSearchQuery changes)', () => {
      const { result } = renderHook(() => useSearch())

      act(() => {
        result.current.setSearchQuery('test')
      })

      expect(mockPush).not.toHaveBeenCalled()
    })

    it('does not update URL when updateUrl is false', () => {
      const { result } = renderHook(() => useSearch({ updateUrl: false }))

      act(() => {
        result.current.setSearchQuery('test')
      })

      expect(mockPush).not.toHaveBeenCalled()
    })
  })

  describe('Edge Cases', () => {
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

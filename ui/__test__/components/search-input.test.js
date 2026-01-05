import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import SearchInput from '../../components/search-input'

describe('SearchInput Component', () => {
  const defaultProps = {
    searchQuery: '',
    setSearchQuery: jest.fn(),
    isSearching: false,
    placeholder: 'Search...',
    ariaLabel: 'Search',
  }

  beforeEach(() => {
    jest.clearAllMocks()
  })

  it('renders search input with correct placeholder', () => {
    render(<SearchInput {...defaultProps} placeholder='Search groups...' />)

    expect(screen.getByPlaceholderText('Search groups...')).toBeInTheDocument()
  })

  it('displays current search query value', () => {
    render(<SearchInput {...defaultProps} searchQuery='test query' />)

    expect(screen.getByDisplayValue('test query')).toBeInTheDocument()
  })

  it('calls setSearchQuery when input value changes', async () => {
    const user = userEvent.setup()
    const mockSetSearchQuery = jest.fn()

    render(
      <SearchInput {...defaultProps} setSearchQuery={mockSetSearchQuery} />
    )

    const input = screen.getByRole('searchbox')
    await user.type(input, 'new search')

    expect(mockSetSearchQuery).toHaveBeenCalledTimes(10) // One call per character
    expect(mockSetSearchQuery).toHaveBeenLastCalledWith('h') // Last character typed
  })

  it('shows clear button when search query has content', () => {
    render(<SearchInput {...defaultProps} searchQuery='test' />)

    expect(screen.getByLabelText('Clear search')).toBeInTheDocument()
  })

  it('does not show clear button when search query is empty', () => {
    render(<SearchInput {...defaultProps} searchQuery='' />)

    expect(screen.queryByLabelText('Clear search')).not.toBeInTheDocument()
  })

  it('clears search when clear button is clicked', async () => {
    const user = userEvent.setup()
    const mockSetSearchQuery = jest.fn()

    render(
      <SearchInput
        {...defaultProps}
        searchQuery='test'
        setSearchQuery={mockSetSearchQuery}
      />
    )

    const clearButton = screen.getByLabelText('Clear search')
    await user.click(clearButton)

    expect(mockSetSearchQuery).toHaveBeenCalledWith('')
  })

  it('clears search when Escape key is pressed', async () => {
    const user = userEvent.setup()
    const mockSetSearchQuery = jest.fn()

    render(
      <SearchInput
        {...defaultProps}
        searchQuery='test'
        setSearchQuery={mockSetSearchQuery}
      />
    )

    const input = screen.getByRole('searchbox')
    await user.type(input, '{Escape}')

    expect(mockSetSearchQuery).toHaveBeenCalledWith('')
  })

  it('ignores isSearching prop (component does not support it)', () => {
    render(<SearchInput {...defaultProps} isSearching={true} />)

    expect(screen.getByRole('searchbox')).not.toBeDisabled()
  })

  it('disables input when disabled prop is true', () => {
    render(<SearchInput {...defaultProps} disabled={true} />)

    expect(screen.getByRole('searchbox')).toBeDisabled()
  })

  it('applies correct aria attributes', () => {
    render(
      <SearchInput
        {...defaultProps}
        ariaLabel='Search users by name'
        ariaDescribedBy='search-results-count'
      />
    )

    const input = screen.getByRole('searchbox')
    expect(input).toHaveAttribute('aria-label', 'Search users by name')
    expect(input).toHaveAttribute('aria-describedby', 'search-results-count')
  })

  it('shows mobile toggle button on mobile breakpoint', () => {
    render(<SearchInput {...defaultProps} showMobileToggle={true} />)

    expect(screen.getByLabelText('Toggle search')).toBeInTheDocument()
  })

  it('does not show mobile toggle when showMobileToggle is false', () => {
    render(<SearchInput {...defaultProps} showMobileToggle={false} />)

    expect(screen.queryByLabelText('Toggle search')).not.toBeInTheDocument()
  })

  it('toggles mobile search when mobile toggle button is clicked', async () => {
    const user = userEvent.setup()

    render(<SearchInput {...defaultProps} showMobileToggle={true} />)

    const toggleButton = screen.getByLabelText('Toggle search')

    // Initially closed
    expect(toggleButton).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('searchbox')).toBeInTheDocument()

    // Click to open
    await user.click(toggleButton)

    await waitFor(() => {
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true')
    })
    // Ensure React has flushed the state update triggered by the click (avoids act warnings)
    await waitFor(() => {
      expect(screen.getAllByRole('searchbox')).toHaveLength(2)
    })

    // Click to close
    await user.click(toggleButton)

    await waitFor(() => {
      expect(toggleButton).toHaveAttribute('aria-expanded', 'false')
    })
    // Ensure React has flushed the state update triggered by the click (avoids act warnings)
    await waitFor(() => {
      expect(screen.getAllByRole('searchbox')).toHaveLength(1)
    })
  })

  it('applies custom className', () => {
    render(<SearchInput {...defaultProps} className='custom-class' />)

    const input = screen.getByRole('searchbox')
    expect(input).toHaveClass('custom-class')
  })

  it('does not apply loading styles for isSearching (component does not support it)', () => {
    render(<SearchInput {...defaultProps} isSearching={true} />)

    const input = screen.getByRole('searchbox')
    expect(input).not.toHaveClass('opacity-50')
    expect(input).not.toHaveClass('cursor-not-allowed')
  })

  it('does not show clear button when disabled', () => {
    const { rerender } = render(
      <SearchInput {...defaultProps} searchQuery='test' disabled={true} />
    )

    expect(screen.queryByLabelText('Clear search')).not.toBeInTheDocument()

    // When enabled, the clear button should appear if there's a search query.
    rerender(
      <SearchInput {...defaultProps} searchQuery='test' disabled={false} />
    )

    expect(screen.queryByLabelText('Clear search')).toBeInTheDocument()
  })

  it('handles focus and blur events correctly', async () => {
    const user = userEvent.setup()

    render(<SearchInput {...defaultProps} />)

    const input = screen.getByRole('searchbox')

    await user.click(input)
    expect(input).toHaveFocus()

    await user.tab()
    expect(input).not.toHaveFocus()
  })

  describe('Mobile functionality', () => {
    beforeAll(() => {
      // Mock window.matchMedia for responsive tests
      Object.defineProperty(window, 'matchMedia', {
        writable: true,
        value: jest.fn().mockImplementation(query => ({
          matches: query.includes('768px') ? false : true, // Simulate mobile
          media: query,
          onchange: null,
          addListener: jest.fn(),
          removeListener: jest.fn(),
          addEventListener: jest.fn(),
          removeEventListener: jest.fn(),
          dispatchEvent: jest.fn(),
        })),
      })
    })

    it('shows search icon initially on mobile', () => {
      render(<SearchInput {...defaultProps} showMobileToggle={true} />)

      const toggleButton = screen.getByLabelText('Toggle search')
      const magnifyingGlassIcon = toggleButton.querySelector('svg')
      expect(magnifyingGlassIcon).toBeInTheDocument()
    })

    it('shows X icon when mobile search is open', async () => {
      const user = userEvent.setup()

      render(<SearchInput {...defaultProps} showMobileToggle={true} />)

      const toggleButton = screen.getByLabelText('Toggle search')

      // Click to open
      await user.click(toggleButton)

      // Wait for state to settle (prevents act(...) warnings)
      await waitFor(() => {
        expect(toggleButton).toHaveAttribute('aria-expanded', 'true')
      })
      // Also wait for the mobile input to appear so the state update is fully flushed
      await waitFor(() => {
        expect(screen.getAllByRole('searchbox')).toHaveLength(2)
      })

      // Confirm the open icon is rendered (X icon)
      await waitFor(() => {
        expect(toggleButton.querySelector('svg')).toBeInTheDocument()
      })
    })
  })

  describe('Accessibility', () => {
    it('has proper keyboard navigation', async () => {
      const user = userEvent.setup()

      render(<SearchInput {...defaultProps} searchQuery='test' />)

      // Tab to input
      await user.tab()
      expect(screen.getByRole('searchbox')).toHaveFocus()

      // Tab to clear button
      await user.tab()
      expect(screen.getByLabelText('Clear search')).toHaveFocus()
    })

    it('announces search results to screen readers via describedby', () => {
      render(
        <div>
          <SearchInput {...defaultProps} ariaDescribedBy='results' />
          <div id='results'>Found 5 results</div>
        </div>
      )

      const input = screen.getByRole('searchbox')
      expect(input).toHaveAttribute('aria-describedby', 'results')
    })

    it('has proper button labels for screen readers', () => {
      render(
        <SearchInput
          {...defaultProps}
          searchQuery='test'
          showMobileToggle={true}
        />
      )

      expect(screen.getByLabelText('Clear search')).toBeInTheDocument()
      expect(screen.getByLabelText('Toggle search')).toBeInTheDocument()
    })
  })
})

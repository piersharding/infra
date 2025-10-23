# Search Functionality Documentation

This document describes the context search functionality that has been implemented in the Infra dashboard, allowing users to filter lists by typing search queries.

## Overview

The search functionality provides real-time filtering of items (groups, users, etc.) with the following features:

- **Real-time search** - Results update as you type
- **Server-side filtering** - Uses API endpoints for accurate results
- **Debounced requests** - Prevents excessive API calls (300ms delay)
- **URL state management** - Search queries persist in browser URL
- **Mobile responsive** - Optimized for both desktop and mobile devices
- **Accessibility** - Full keyboard navigation and screen reader support
- **Pagination reset** - Automatically returns to page 1 when searching

## Implementation Components

### 1. SearchInput Component (`/components/search-input.js`)

A reusable React component that provides the search interface.

**Props:**
- `searchQuery` (string) - Current search query value
- `setSearchQuery` (function) - Function to update search query
- `isSearching` (boolean) - Whether a search is in progress
- `placeholder` (string) - Input placeholder text
- `ariaLabel` (string) - Accessibility label
- `ariaDescribedBy` (string) - ID of element describing search results
- `disabled` (boolean) - Whether input is disabled
- `className` (string) - Additional CSS classes
- `showMobileToggle` (boolean) - Whether to show mobile search toggle

**Features:**
- Search icon and clear button
- Mobile-responsive design with toggle
- Keyboard shortcuts (Escape to clear)
- Loading states
- Accessibility attributes

### 2. useSearch Hook (`/lib/useSearch.js`)

A custom React hook that manages search state, debouncing, and URL synchronization.

**Parameters:**
- `debounceMs` (number) - Debounce delay in milliseconds (default: 300)
- `searchParam` (string) - URL parameter name for search (default: 'search')
- `pageParam` (string) - URL parameter name for page (default: 'p')
- `resetPageOnSearch` (boolean) - Whether to reset to page 1 on search (default: true)
- `updateUrl` (boolean) - Whether to update URL with search params (default: true)

**Returns:**
- `searchQuery` - Current search input value
- `debouncedSearchQuery` - Debounced search value for API calls
- `isSearching` - Loading state during debounce
- `hasActiveSearch` - Whether there's an active search
- `setSearchQuery` - Function to update search query
- `clearSearch` - Function to clear search
- `buildSearchParams` - Utility to build URL search parameters
- `buildApiUrl` - Utility to build API URLs with search
- `getEmptyMessage` - Generate appropriate empty state message
- `getResultMessage` - Generate search results summary

## Usage Examples

### Basic Implementation

```javascript
import { useSearch } from '../lib/useSearch'
import SearchInput from '../components/search-input'

function MyListPage() {
  const router = useRouter()
  const page = Math.max(parseInt(router.query.p) || 1, 1)
  const limit = 50

  // Initialize search functionality
  const {
    searchQuery,
    setSearchQuery,
    isSearching,
    buildApiUrl,
    getEmptyMessage,
    getResultMessage,
  } = useSearch()

  // Build API URL with search and pagination
  const apiUrl = buildApiUrl('/api/items', {
    page: page.toString(),
    limit: limit.toString(),
  })

  // Fetch data
  const { data: { items, totalPages, totalCount } = {} } = useSWR(apiUrl)

  return (
    <div>
      <header className='my-6'>
        <div className='flex items-center justify-between'>
          <div className='flex flex-1 items-center space-x-4'>
            <h1 className='font-display text-xl font-medium'>Items</h1>
            <SearchInput
              searchQuery={searchQuery}
              setSearchQuery={setSearchQuery}
              isSearching={isSearching}
              placeholder='Search items...'
              ariaLabel='Search items by name'
              ariaDescribedBy='search-results-count'
            />
          </div>
          {/* Action buttons */}
        </div>
      </header>

      {/* Screen reader result summary */}
      <div id='search-results-count' className='sr-only'>
        {getResultMessage(totalCount || 0, 'item')}
      </div>

      {/* Table with data */}
      <Table
        data={items}
        empty={getEmptyMessage('No items')}
        // ... other props
      />
    </div>
  )
}
```

### Custom Configuration

```javascript
// Custom debounce timing and URL parameters
const search = useSearch({
  debounceMs: 500,          // Wait 500ms before searching
  searchParam: 'query',     // Use 'query' instead of 'search' in URL
  pageParam: 'page',        // Use 'page' instead of 'p' in URL
  resetPageOnSearch: false, // Don't reset to page 1 when searching
  updateUrl: false,         // Don't update URL (for modal searches, etc.)
})
```

## API Requirements

For the search functionality to work, your API endpoints must support name-based filtering:

### Groups API
```
GET /api/groups?name=search-term&page=1&limit=50
```

### Users API  
```
GET /api/users?name=search-term&page=1&limit=50
```

The `name` parameter should perform case-insensitive partial matching on the item's name field.

## Styling and Responsive Design

### Desktop
- Search input appears inline with the page header
- Clear button (X) appears when there's search content
- Loading state dims the input during debounce

### Mobile
- Search icon appears in the header
- Tapping the icon reveals the search input below the header
- Full-width search input for easy typing on small screens

### Accessibility Features
- Proper ARIA labels and descriptions
- Keyboard navigation support
- Screen reader announcements for search results
- Focus management for clear and mobile toggle buttons

## Best Practices

1. **Always use debounced search** - Use the `debouncedSearchQuery` for API calls, not `searchQuery`

2. **Provide result feedback** - Use `getResultMessage()` for screen readers and visual feedback

3. **Handle empty states** - Use `getEmptyMessage()` to show appropriate messages for no results vs. no search

4. **Mobile optimization** - Always test search functionality on mobile devices

5. **URL state management** - Let the hook handle URL updates to maintain search state on page refresh

6. **Error handling** - Handle API errors gracefully when search requests fail

7. **Performance** - The 300ms debounce prevents excessive API calls while still feeling responsive

## Browser Support

The search functionality supports all modern browsers that support:
- ES6+ JavaScript features
- React 18+
- Next.js 15+
- CSS Grid and Flexbox

## Troubleshooting

### Search not working
- Check that your API endpoint supports the `name` query parameter
- Verify the API returns data in the expected format (`{ items: [], totalCount: number, totalPages: number }`)
- Ensure SWR is properly configured with the correct API URL

### URL not updating
- Check that `updateUrl` is not set to `false`
- Verify that Next.js router is available in your component context

### Mobile search not responsive
- Ensure you're using the correct Tailwind CSS breakpoint classes (`md:hidden`, `md:block`)
- Test on actual mobile devices or browser dev tools mobile emulation

### Performance issues
- Increase debounce time if API is slow: `useSearch({ debounceMs: 500 })`
- Consider implementing client-side caching with SWR's built-in cache
- Monitor network requests in browser dev tools
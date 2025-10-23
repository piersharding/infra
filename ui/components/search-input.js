import { useState, useRef } from 'react'
import {
    MagnifyingGlassIcon,
    XMarkIcon,
} from '@heroicons/react/24/outline'

export default function SearchInput({
    searchQuery,
    setSearchQuery,
    onSearch,
    placeholder = 'Search...',
    ariaLabel = 'Search',
    ariaDescribedBy,
    disabled = false,
    className = '',
    showMobileToggle = true,
}) {
    const [mobileSearchOpen, setMobileSearchOpen] = useState(false)
    const inputRef = useRef(null)

    const handleClear = () => {
        setSearchQuery('')
        setMobileSearchOpen(false)
        if (onSearch) {
            onSearch()
        }
    }

    const handleKeyDown = e => {
        if (e.key === 'Escape') {
            handleClear()
        } else if (e.key === 'Enter' && onSearch) {
            e.preventDefault()
            onSearch()
        }
    }

    // Render the search input - defined inline to avoid component recreation
    const renderSearchField = (isMobile = false) => (
        <div className={`relative ${isMobile ? 'w-full' : 'max-w-xs'}`}>
            <MagnifyingGlassIcon className='absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400' />
            <input
                ref={inputRef}
                type='search'
                placeholder={placeholder}
                aria-label={ariaLabel}
                aria-describedby={ariaDescribedBy}
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
                onKeyDown={handleKeyDown}
                disabled={disabled}
                className={`block w-full rounded-md border-gray-300 pl-10 pr-10 py-2 text-sm placeholder-gray-500 focus:border-blue-500 focus:ring-blue-500 ${
                    disabled ? 'opacity-50 cursor-not-allowed' : ''
                } ${className}`}
            />
            {searchQuery && !disabled && (
                <button
                    onClick={handleClear}
                    className='absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 rounded'
                    aria-label='Clear search'
                    type='button'
                >
                    <XMarkIcon className='h-4 w-4' />
                </button>
            )}
        </div>
    )

    return (
        <>
            {/* Desktop search */}
            <div className='hidden md:block'>
                {renderSearchField()}
            </div>

            {/* Mobile search toggle */}
            {showMobileToggle && (
                <div className='flex items-center md:hidden'>
                    <button
                        onClick={() => setMobileSearchOpen(!mobileSearchOpen)}
                        className='p-2 rounded-md text-gray-400 hover:text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2'
                        aria-label='Toggle search'
                        aria-expanded={mobileSearchOpen}
                    >
                        {mobileSearchOpen ? (
                            <XMarkIcon className='h-5 w-5' />
                        ) : (
                            <MagnifyingGlassIcon className='h-5 w-5' />
                        )}
                    </button>
                </div>
            )}

            {/* Mobile search input */}
            {showMobileToggle && mobileSearchOpen && (
                <div className='absolute left-0 right-0 top-full z-10 bg-white border-b border-gray-200 px-6 py-3 shadow-sm md:hidden'>
                    {renderSearchField(true)}
                </div>
            )}

            {/* Always visible mobile search (when showMobileToggle is false) */}
            {!showMobileToggle && (
                <div className='block md:hidden'>
                    {renderSearchField(true)}
                </div>
            )}
        </>
    )
}
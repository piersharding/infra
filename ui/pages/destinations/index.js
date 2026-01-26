import useSWR from 'swr'
import Head from 'next/head'
import Link from 'next/link'
import { useRouter } from 'next/router'
import { useState, useMemo } from 'react'

import { useUser } from '../../lib/hooks'
import { CommandLineIcon } from '@heroicons/react/24/solid'
import { TrashIcon, PlusIcon } from '@heroicons/react/24/outline'

import Table from '../../components/table'
import Dashboard from '../../components/layouts/dashboard'
import DeleteModal from '../../components/delete-modal'
import Loader from '../../components/loader'
import SearchInput from '../../components/search-input'
import { useSearch } from '../../lib/useSearch'

export default function Destinations() {
  const router = useRouter()
  const page = Math.max(parseInt(router.query.p) || 1, 1)
  const limit = 50

  const { isAdmin, isAdminLoading } = useUser()

  // Search functionality
  const {
    searchQuery,
    setSearchQuery,
    executeSearch,
    buildApiUrl,
    getEmptyMessage,
    getResultMessage,
  } = useSearch()

  // Build API URL with pagination
  const apiUrl = useMemo(
    () =>
      buildApiUrl('/api/destinations', {
        page: page.toString(),
        limit: limit.toString(),
      }),
    [buildApiUrl, page, limit]
  )

  // Fetch destinations data
  const { data: { items: destinations, totalCount, totalPages } = {}, mutate } =
    useSWR(apiUrl)
  const [openSelectedDeleteModal, setOpenSelectedDeleteModal] = useState(false)
  const [selectedDeleteId, setSelectedDeleteId] = useState(null)

  // Generate appropriate messages
  const emptyMessage = getEmptyMessage('No infrastructure')
  const resultText = getResultMessage(totalCount || 0, 'destination')

  if (isAdminLoading) {
    return (
      <div className='flex h-screen items-center justify-center'>
        <Loader className='h-20 w-20' />
      </div>
    )
  }

  return (
    <div className='mb-10'>
      <Head>
        <title>Infrastructure - Infra</title>
      </Head>

      <header className='my-6'>
        <div className='flex items-center justify-between'>
          <div className='flex flex-1 items-center space-x-4'>
            <h1 className='py-1 font-display text-xl font-medium'>
              Infrastructure
            </h1>
            <SearchInput
              searchQuery={searchQuery}
              setSearchQuery={setSearchQuery}
              onSearch={executeSearch}
              placeholder='Search infrastructure... (press Enter)'
              ariaLabel='Search destinations by name'
              ariaDescribedBy='search-results-count'
            />
          </div>

          {/* Add dialog button */}
          {isAdmin && (
            <Link
              href='/destinations/add'
              className='inline-flex items-center rounded-md border border-transparent bg-black  px-4 py-2 text-xs font-medium text-white shadow-sm hover:cursor-pointer hover:bg-gray-800'
            >
              <PlusIcon className='mr-1 h-3 w-3' /> Connect Cluster
            </Link>
          )}
        </div>
      </header>

      {/* Search results summary for screen readers */}
      <div id='search-results-count' className='sr-only'>
        {resultText}
      </div>

      {/* Table */}
      <div className='flex min-h-0 flex-1 flex-col'>
        <Table
          href={row => `/destinations/${row.original.id}`}
          count={totalCount}
          pageCount={totalPages}
          pageIndex={page - 1}
          pageSize={limit}
          data={destinations}
          empty={emptyMessage}
          onPageChange={({ pageIndex }) => {
            const newQuery = { ...router.query, p: pageIndex + 1 }
            router.push({
              pathname: router.pathname,
              query: newQuery,
            })
          }}
          columns={[
            {
              cell: info => (
                <div className='flex flex-row items-center py-1'>
                  <div className='mr-3 flex h-9 w-9 flex-none items-center justify-center rounded-md border border-gray-200'>
                    {info.row.original.kind === 'ssh' ? (
                      <CommandLineIcon className='h-5 w-5 text-gray-800' />
                    ) : (
                      <img
                        alt='kubernetes icon'
                        className='h-5'
                        src={`/kubernetes.svg`}
                      />
                    )}
                  </div>
                  <div className='flex flex-col'>
                    <div className='text-sm font-medium text-gray-700'>
                      {info.getValue()}
                    </div>

                    <div className='text-2xs text-gray-500'>
                      {info.row.original.connection.url === ''
                        ? '-'
                        : info.row.original.connection.url}
                    </div>
                  </div>
                </div>
              ),
              header: () => <span>Name</span>,
              accessorKey: 'name',
            },
            {
              cell: info => (
                <span className='hidden lg:table-cell'>{info.getValue()}</span>
              ),
              header: () => (
                <span className='hidden lg:table-cell'>Connector Version</span>
              ),
              accessorKey: 'version',
            },
            {
              cell: info => (
                <div className='flex items-center py-2'>
                  <div
                    className={`h-2 w-2 flex-none rounded-full border ${
                      info.getValue()
                        ? info.row.original.connection.url === ''
                          ? 'animate-pulse border-yellow-500 bg-yellow-500'
                          : 'border-teal-400 bg-teal-400'
                        : 'border-gray-200 bg-gray-200'
                    }`}
                  />
                  <span className='flex-none px-2'>
                    {info.getValue()
                      ? info.row.original.connection.url === ''
                        ? 'Pending'
                        : 'Connected'
                      : 'Disconnected'}
                  </span>
                </div>
              ),
              header: () => <span>Status</span>,
              accessorKey: 'connected',
            },
            {
              id: 'delete',
              cell: function Cell(info) {
                return (
                  info.row.original.kind === 'ssh' && (
                    <div className='group invisible rounded-md bg-transparent group-hover:visible'>
                      <button
                        type='button'
                        onClick={() => {
                          setSelectedDeleteId(info.row.original.id)
                          setOpenSelectedDeleteModal(true)
                        }}
                        className='flex items-center text-xs font-medium text-red-500 hover:text-red-500/50'
                      >
                        <TrashIcon className='mr-2 h-3.5 w-3.5' />
                        <span className='hidden sm:block'>Remove</span>
                      </button>
                    </div>
                  )
                )
              },
            },
          ]}
        />
      </div>
      <DeleteModal
        open={openSelectedDeleteModal}
        setOpen={setOpenSelectedDeleteModal}
        onSubmit={async () => {
          await fetch(`/api/destinations/${selectedDeleteId}`, {
            method: 'DELETE',
          })

          await mutate()
          setSelectedDeleteId(null)
          setOpenSelectedDeleteModal(false)
        }}
        title={'Remove destination'}
        message={<>Are you sure you want to remove the selected destination?</>}
      />
    </div>
  )
}

Destinations.layout = function (page) {
  return <Dashboard>{page}</Dashboard>
}

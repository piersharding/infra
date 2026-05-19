import Head from 'next/head'
import Link from 'next/link'
import useSWR, { mutate } from 'swr'
import { useRouter } from 'next/router'
import { useState, useMemo } from 'react'

import { PlusIcon, PencilIcon, TrashIcon, EyeDropperIcon } from '@heroicons/react/24/outline'

import Table from '../../components/table'
import Dashboard from '../../components/layouts/dashboard'
import DeleteModal from '../../components/delete-modal'
import SearchInput from '../../components/search-input'
import Loader from '../../components/loader'
import Notification from '../../components/notification'
import { useUser } from '../../lib/hooks'
import { useSearch } from '../../lib/useSearch'

function jsonBody(res) {
  return res.json()
}

export default function GroupsMapping() {
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

  // Build API URL with pagination and optional name filter
  const apiUrl = useMemo(
    () => {
      const params = new URLSearchParams({
        page: page.toString(),
        limit: limit.toString(),
      })
      if (searchQuery) {
        params.set('name', searchQuery)
      }
      return `/api/group-mappings?${params.toString()}`
    },
    [page, limit, searchQuery]
  )

  // Fetch group mappings data
  const { data: { items: mappings = [], totalCount, totalPages } = {}, mutate } = useSWR(apiUrl)
  
  const [deleteModalOpen, setDeleteModalOpen] = useState(false)
  const [selectedMappingId, setSelectedMappingId] = useState(null)
  const [notification, setNotification] = useState(null)

  // Generate appropriate messages
  const emptyMessage = getEmptyMessage('No group mappings')
  const resultText = getResultMessage(totalCount || 0, 'mapping')

  async function handleDelete(id) {
    try {
      const res = await fetch(`/api/group-mappings/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete mapping')
      setNotification({ type: 'success', message: 'Group mapping deleted successfully' })
      mutate(apiUrl)
    } catch (e) {
      setNotification({ type: 'error', message: e.message || 'Failed to delete group mapping' })
    }
  }

  if (isAdminLoading) {
    return (
      <div className='flex h-screen items-center justify-center'>
        <Loader className='h-20 w-20' />
      </div>
    )
  }

  function previewTemplate(template, sampleGroup) {
    if (!template || !sampleGroup) return '—'
    let result = template
    // Show $N placeholders that weren't substituted
    const regex = /\$[1-9]\d*/g
    const matches = [...result.matchAll(regex)]
    
    // Simple preview: replace single-digit references with [group] placeholder
    for (const match of matches) {
      const refNum = parseInt(match[0].substring(1), 10)
      if (refNum <= sampleGroup.split('-').length + 2) {
        result = result.replace(new RegExp('\\$' + refNum, 'g'), `[group-${refNum}]`)
      }
    }
    
    return result
  }

  function handleDeleteClick(id) {
    setSelectedMappingId(id)
    setDeleteModalOpen(true)
  }

  function closeModal() {
    setDeleteModalOpen(false)
    setSelectedMappingId(null)
  }

  // Clear notification after 5 seconds
  useState(() => {
    if (notification) {
      const timer = setTimeout(() => setNotification(null), 5000)
      return () => clearTimeout(timer)
    }
  })

  return (
    <div className='mb-10'>
      <Head>
        <title>Groups Mapping - Infra</title>
      </Head>

      {/* Notification */}
      {notification && (
        <Notification type={notification.type} message={notification.message} onClose={() => setNotification(null)} />
      )}

      <header className='my-6'>
        <div className='flex items-center justify-between'>
          <div className='flex flex-1 items-center space-x-4'>
            <h1 className='py-1 font-display text-xl font-medium'>Groups Mapping</h1>
            {resultText && <span className='text-sm text-gray-500'>{resultText}</span>}
          </div>
          <Link
            href='/groups-mapping/add'
            className='ml-4 inline-flex items-center self-end rounded-md border border-transparent bg-black px-4 py-2 text-xs font-medium text-white shadow-sm hover:cursor-pointer hover:bg-gray-800'
          >
            <PlusIcon className='mr-1 h-3 w-3' /> Add Rule
          </Link>
        </div>
      </header>

      {/* Search */}
      {totalCount > 5 && (
        <div className='mb-4'>
          <SearchInput
            searchQuery={searchQuery}
            setSearchQuery={setSearchQuery}
            onSearch={executeSearch}
            placeholder='Search rules... (press Enter)'
            ariaLabel='Search group mappings by rule name'
          />
        </div>
      )}

      {/* Empty state */}
      {totalCount === 0 && !searchQuery && (
        <div className='mt-8 text-center'>
          <p className='text-gray-500'>{emptyMessage}</p>
        </div>
      )}

      {/* Table */}
      {totalCount > 0 && (
        <Table
          data={mappings.map(m => ({
            id: m.id,
            ruleName: m.rule_name,
            sourceGroupRegex: m.source_group_regex,
            destinationType: m.destination_type === 'kubernetes' ? 'Kubernetes' : 'SSH',
            nameTemplatePreview: previewTemplate(m.name_template, 'team-platform'),
            roleTemplatePreview: m.role_template ? previewTemplate(m.role_template, 'team-platform') : null,
          }))}
          columns={[
            { key: 'ruleName', label: 'Rule Name' },
            { key: 'sourceGroupRegex', label: 'Source Group Regex' },
            { key: 'destinationType', label: 'Destination Type' },
            { key: 'nameTemplatePreview', label: 'Generated Resource Example' },
            ...(mappings.some(m => m.role_template) ? [{ key: 'roleTemplatePreview', label: 'Role Template Preview' }] : []),
          ]}
        />
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className='mt-4 flex items-center justify-between'>
          <p className='text-sm text-gray-500'>Page {page} of {totalPages}</p>
          <div className='flex space-x-2'>
            {Array.from({ length: totalPages }, (_, i) => (
              <Link
                key={i + 1}
                href={{ pathname: router.pathname, query: { ...router.query, p: i + 1 } }}
                passHref
              >
                <span className={`inline-block rounded px-3 py-1 text-sm ${page === i + 1 ? 'bg-gray-200 font-medium' : 'hover:bg-gray-100 cursor-pointer'}`}>
                  {i + 1}
                </span>
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Delete Confirmation Modal */}
      <DeleteModal
        isOpen={deleteModalOpen}
        onClose={closeModal}
        onConfirm={() => handleDelete(selectedMappingId)}
        title='Delete Group Mapping Rule'
        message={`Are you sure you want to delete the mapping "${selectedMappingId}"? This action cannot be undone.`}
      />
    </div>
  )
}

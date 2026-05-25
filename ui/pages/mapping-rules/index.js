import Head from 'next/head'
import Link from 'next/link'
import useSWR, { mutate } from 'swr'
import { useRouter } from 'next/router'
import { useState, useMemo } from 'react'

import {
  PlusIcon,
  PencilIcon,
  TrashIcon,
  EyeDropperIcon,
} from '@heroicons/react/24/outline'
import { Dialog } from '@headlessui/react'

import Table from '../../components/table'
import Dashboard from '../../components/layouts/dashboard'
import DeleteModal from '../../components/delete-modal'
import SearchInput from '../../components/search-input'
import Notification from '../../components/notification'
import { useUser } from '../../lib/hooks'
import { useSearch } from '../../lib/useSearch'
import { previewRegex, previewTemplate } from '../../lib/mappingRules'

function jsonBody(res) {
  return res.json()
}

function AddMappingRuleDialog({ open, setOpen, groups, onMutate }) {
  const [ruleName, setRuleName] = useState('')
  const [sourceGroupRegex, setSourceGroupRegex] = useState('')
  const [destinationType, setDestinationType] = useState('kubernetes')
  const [nameTemplate, setNameTemplate] = useState('')
  const [namespaceTemplate, setNamespaceTemplate] = useState('')
  const [roleTemplate, setRoleTemplate] = useState('')
  const [errors, setErrors] = useState({})
  const [submitting, setSubmitting] = useState(false)

  const matchedGroups = useMemo(() => {
    if (!sourceGroupRegex || !groups?.length) return []
    try {
      return previewRegex(
        sourceGroupRegex,
        groups.map(g => g.name)
      )
    } catch {
      return []
    }
  }, [sourceGroupRegex, groups])

  async function handleSubmit(e) {
    e.preventDefault()
    const newErrors = {}
    if (!ruleName.trim()) newErrors.rule_name = 'Rule name is required'
    if (!sourceGroupRegex.trim())
      newErrors.source_group_regex = 'Group matching regex is required'
    else {
      try {
        new RegExp(sourceGroupRegex)
      } catch {
        newErrors.source_group_regex = 'Invalid regular expression'
      }
    }
    if (!nameTemplate.trim())
      newErrors.name_template = 'Destination name template is required'
    if (destinationType === 'kubernetes' && !roleTemplate.trim())
      newErrors.role_template = 'Role template is required for Kubernetes'

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors)
      return
    }

    setSubmitting(true)
    try {
      const body = {
        rule_name: ruleName.trim(),
        source_group_regex: sourceGroupRegex,
        destination_type: destinationType,
        name_template: nameTemplate,
      }
      if (destinationType === 'kubernetes') body.role_template = roleTemplate
      if (namespaceTemplate && destinationType === 'kubernetes')
        body.namespace_template = namespaceTemplate

      const res = await fetch('/api/mapping-rules', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        const errBody = await res.json().catch(() => null)
        setErrors({
          submit: Array.isArray(errBody?.message)
            ? errBody.message.join(', ')
            : errBody?.message || 'Failed to save',
        })
        return
      }
      await res.json()
      if (onMutate) onMutate()
      setOpen(false)
      setRuleName('')
      setSourceGroupRegex('')
      setNameTemplate('')
      setRoleTemplate('')
      setNamespaceTemplate('')
      setErrors({})
    } catch (e) {
      setErrors({ submit: e.message })
    }
    setSubmitting(false)
  }

  return (
    <>
      {open && (
        <Dialog
          as='div'
          className='relative z-50'
          open={open}
          onClose={() => setOpen(false)}
        >
          <div className='fixed inset-0 flex items-center justify-center p-4 bg-black/30'>
            <Dialog.Panel className='w-full max-w-lg transform overflow-hidden rounded-xl bg-white p-6 shadow-xl'>
              <div className='flex items-center justify-between mb-4'>
                <h2 className='text-base font-semibold text-gray-900'>
                  Add Mapping Rule
                </h2>
                <button
                  type='button'
                  onClick={() => setOpen(false)}
                  className='text-gray-400 hover:text-gray-600 cursor-pointer'
                >
                  &#x2715;
                </button>
              </div>
              <form onSubmit={handleSubmit} className='space-y-4'>
                {errors.submit && (
                  <div className='rounded-md bg-red-50 p-3 text-sm text-red-700'>
                    {typeof errors.submit === 'string'
                      ? errors.submit
                      : JSON.stringify(errors.submit)}
                  </div>
                )}
                <div>
                  <label
                    htmlFor='mr-name'
                    className='text-xs font-medium text-gray-600'
                  >
                    Rule Name
                  </label>
                  <input
                    id='mr-name'
                    value={ruleName}
                    onChange={e => setRuleName(e.target.value)}
                    placeholder='e.g., team-platform-access'
                    className={`mt-1 block w-full rounded-md border ${errors.rule_name ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 sm:text-sm`}
                  />
                  {errors.rule_name && (
                    <p className='mt-1 text-xs text-red-500'>
                      {errors.rule_name}
                    </p>
                  )}
                </div>
                <div>
                  <label
                    htmlFor='mr-type'
                    className='text-xs font-medium text-gray-600'
                  >
                    Destination Type
                  </label>
                  <select
                    id='mr-type'
                    value={destinationType}
                    onChange={e => setDestinationType(e.target.value)}
                    className='mt-1 block w-full rounded-md border border-gray-300 shadow-sm sm:text-sm'
                  >
                    <option value='kubernetes'>Kubernetes</option>
                    <option value='ssh'>SSH</option>
                  </select>
                </div>
                <div>
                  <label
                    htmlFor='mr-regex'
                    className='text-xs font-medium text-gray-600'
                  >
                    Group Matching Regex
                  </label>
                  <input
                    id='mr-regex'
                    type='search'
                    value={sourceGroupRegex}
                    onChange={e => setSourceGroupRegex(e.target.value)}
                    placeholder='e.g., ^team-(.*)$'
                    className={`mt-1 block w-full rounded-md border ${errors.source_group_regex ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 sm:text-sm`}
                  />
                  {errors.source_group_regex && (
                    <p className='mt-1 text-xs text-red-500'>
                      {errors.source_group_regex}
                    </p>
                  )}
                </div>
                {matchedGroups.length > 0 && (
                  <div className='rounded-md bg-green-50 p-2 text-xs'>
                    <span className='font-medium'>Matches:</span>{' '}
                    {matchedGroups.join(', ')}
                  </div>
                )}
                <div>
                  <label
                    htmlFor='mr-template'
                    className='text-xs font-medium text-gray-600'
                  >
                    Destination Name Template ($N for capture groups)
                  </label>
                  <input
                    id='mr-template'
                    value={nameTemplate}
                    onChange={e => setNameTemplate(e.target.value)}
                    placeholder='e.g., cluster-$1-prod'
                    className={`mt-1 block w-full rounded-md border ${errors.name_template ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 sm:text-sm`}
                  />
                  {errors.name_template && (
                    <p className='mt-1 text-xs text-red-500'>
                      {errors.name_template}
                    </p>
                  )}
                </div>
                {destinationType === 'kubernetes' && (
                  <>
                    <div>
                      <label
                        htmlFor='mr-role'
                        className='text-xs font-medium text-gray-600'
                      >
                        Role Template
                      </label>
                      <input
                        id='mr-role'
                        value={roleTemplate}
                        onChange={e => setRoleTemplate(e.target.value)}
                        placeholder='e.g., $1-admin'
                        className={`mt-1 block w-full rounded-md border ${errors.role_template ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 sm:text-sm`}
                      />
                      {errors.role_template && (
                        <p className='mt-1 text-xs text-red-500'>
                          {errors.role_template}
                        </p>
                      )}
                    </div>
                    <div>
                      <label
                        htmlFor='mr-ns'
                        className='text-xs font-medium text-gray-600'
                      >
                        Namespace Template (optional)
                      </label>
                      <input
                        id='mr-ns'
                        value={namespaceTemplate}
                        onChange={e => setNamespaceTemplate(e.target.value)}
                        placeholder='Optional: e.g., $1-ns'
                        className='mt-1 block w-full rounded-md border border-gray-300 shadow-sm sm:text-sm'
                      />
                    </div>
                  </>
                )}
                {destinationType === 'ssh' && (
                  <div className='rounded-md bg-yellow-50 p-2 text-xs'>
                    For SSH destinations, the privilege is always "connect" and
                    role template is not used.
                  </div>
                )}
                <div className='flex justify-end space-x-3 pt-4'>
                  <button
                    type='button'
                    onClick={() => setOpen(false)}
                    className='rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium hover:bg-gray-50 cursor-pointer'
                  >
                    Cancel
                  </button>
                  <button
                    type='submit'
                    disabled={submitting}
                    className={`rounded-md border border-transparent bg-black px-4 py-2 text-sm font-medium text-white ${submitting ? 'opacity-50 cursor-not-allowed' : 'hover:bg-gray-800 cursor-pointer'}`}
                  >
                    {submitting ? 'Creating...' : 'Create Rule'}
                  </button>
                </div>
              </form>
            </Dialog.Panel>
          </div>
        </Dialog>
      )}
    </>
  )
}

export default function GroupsMapping() {
  const router = useRouter()
  const page = Math.max(parseInt(router.query.p) || 1, 1)
  const limit = 50

  const { isAdmin } = useUser()

  // Search functionality
  const {
    searchQuery,
    setSearchQuery,
    executeSearch,
    buildApiUrl,
    getEmptyMessage,
    getResultMessage,
  } = useSearch()

  // Memoized: builds the API URL string with pagination params and optional name search query.
  const apiUrl = useMemo(() => {
    const params = new URLSearchParams({
      page: page.toString(),
      limit: limit.toString(),
    })
    if (searchQuery) {
      params.set('name', searchQuery)
    }
    return `/api/mapping-rules?${params.toString()}`
  }, [page, limit, searchQuery])

  // SWR hook fetches mapping rules from the API. Mutate is used to invalidate cache after CRUD operations.
  const { data: mappingsData } = useSWR(apiUrl) || {}
  const items = mappingsData?.items || []
  const totalPages = mappingsData?.totalPages || 0
  const totalCount = mappingsData?.totalCount || 0

  const { data: groupsData } = useSWR('/api/groups') || {}
  const groups = (groupsData?.items || []).map(g => g.name)

  const [deleteModalOpen, setDeleteModalOpen] = useState(false)
  const [selectedMappingId, setSelectedMappingId] = useState(null)
  const [selectedMappingName, setSelectedMappingName] = useState('')
  const [showNotification, setShowNotification] = useState(false)
  const [notificationMessage, setNotificationMessage] = useState('')
  const [addOpen, setAddOpen] = useState(false)

  // Determine empty-state and result-count messages for the UI.
  const emptyMessage = getEmptyMessage('No mapping rules')
  const resultText = getResultMessage(totalCount || 0, 'mapping rules')

  async function handleDelete(id) {
    try {
      const res = await fetch(`/api/mapping-rules/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete mapping')
      setShowNotification(true)
      setNotificationMessage('Group mapping deleted successfully')
      mutate(apiUrl)
    } catch (e) {
      setShowNotification(true)
      setNotificationMessage(e.message || 'Failed to delete mapping rule')
    }
    closeModal()
  }

  async function handleRowDeleteClick(row) {
    const original = row.original
    setSelectedMappingId(original.id)
    setSelectedMappingName(original.ruleName || '')
    setDeleteModalOpen(true)
  }

  function closeModal() {
    setDeleteModalOpen(false)
    setSelectedMappingId(null)
    setSelectedMappingName('')
  }

  // Clear notification after 5 seconds
  useState(() => {
    if (showNotification) {
      const timer = setTimeout(() => setShowNotification(false), 5000)
      return () => clearTimeout(timer)
    }
  })

  return (
    <div className='mb-10'>
      <Head>
        <title>Mapping Rules - Infra</title>
      </Head>

      {/* Notification */}
      <Notification
        show={showNotification}
        text={notificationMessage}
        setShow={setShowNotification}
        setClearNotification={() => {}}
      />

      <header className='my-6'>
        <div className='flex items-center justify-between'>
          <div className='flex flex-1 items-center space-x-4'>
            <h1 className='py-1 font-display text-xl font-medium'>
              Mapping Rules
            </h1>
            {resultText && (
              <span className='text-sm text-gray-500'>{resultText}</span>
            )}
          </div>
          <button
            type='button'
            onClick={() => setAddOpen(true)}
            className='ml-4 inline-flex items-center self-end rounded-md border border-transparent bg-black px-4 py-2 text-xs font-medium text-white shadow-sm hover:cursor-pointer hover:bg-gray-800'
          >
            <PlusIcon className='mr-1 h-3 w-3' /> Add Rule
          </button>
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
            ariaLabel='Search mapping rules by rule name'
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
          data={items.map(m => ({
            id: m.id,
            ruleName: m.rule_name,
            sourceGroupRegex: m.source_group_regex,
            destinationType:
              m.destination_type === 'kubernetes' ? 'Kubernetes' : 'SSH',
            nameTemplatePreview: previewTemplate(
              m.name_template,
              'team-platform'
            ),
            roleTemplatePreview: m.role_template
              ? previewTemplate(m.role_template, 'team-platform')
              : '-',
            namespaceTemplatePreview: m.namespace_template
              ? previewTemplate(m.namespace_template, 'team-platform')
              : '-',
          }))}
          columns={[
            { header: () => <span>Rule Name</span>, accessorKey: 'ruleName' },
            {
              header: () => <span>Group Matching Regex</span>,
              accessorKey: 'sourceGroupRegex',
            },
            {
              header: () => <span>Destination Type</span>,
              accessorKey: 'destinationType',
            },
            {
              header: () => <span>Destination Name Template</span>,
              accessorKey: 'nameTemplatePreview',
            },
            {
              header: () => <span>Role Template</span>,
              accessorKey: 'roleTemplatePreview',
            },
            {
              header: () => <span>Namespace Template</span>,
              accessorKey: 'namespaceTemplatePreview',
            },
            {
              id: 'delete',
              cell: function Cell(info) {
                return (
                  <div className='group invisible rounded-md bg-transparent group-hover:visible'>
                    <button
                      type='button'
                      onClick={() => handleRowDeleteClick(info.row)}
                      className='flex items-center text-xs font-medium text-red-500 hover:text-red-400'
                    >
                      <TrashIcon className='mr-2 h-3.5 w-3.5' />
                      <span className='hidden sm:block'>Remove</span>
                    </button>
                  </div>
                )
              },
            },
          ]}
        />
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className='mt-4 flex items-center justify-between'>
          <p className='text-sm text-gray-500'>
            Page {page} of {totalPages}
          </p>
          <div className='flex space-x-2'>
            {Array.from({ length: totalPages }, (_, i) => (
              <Link
                key={i + 1}
                href={{
                  pathname: router.pathname,
                  query: { ...router.query, p: i + 1 },
                }}
                passHref
              >
                <span
                  className={`inline-block rounded px-3 py-1 text-sm ${page === i + 1 ? 'bg-gray-200 font-medium' : 'hover:bg-gray-100 cursor-pointer'}`}
                >
                  {i + 1}
                </span>
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Delete Confirmation Modal */}
      <DeleteModal
        open={deleteModalOpen}
        setOpen={setDeleteModalOpen}
        onSubmit={() => handleDelete(selectedMappingId)}
        title='Delete Mapping Rule'
        message={
          <>{`Are you sure you want to delete the mapping "${selectedMappingName}"? This will remove any auto-granted access created by this rule.`}</>
        }
      />

      {/* Add Rule Modal */}
      <AddMappingRuleDialog
        open={addOpen}
        setOpen={setAddOpen}
        groups={groups.map(g => g.name)}
        onMutate={() => mutate(apiUrl)}
      />
    </div>
  )
}

GroupsMapping.layout = function (page) {
  return <Dashboard>{page}</Dashboard>
}

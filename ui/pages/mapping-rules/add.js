import Head from 'next/head'
import Link from 'next/link'
import { useRouter } from 'next/router'
import { useState, useMemo, useCallback } from 'react'

import { ArrowLeftIcon } from '@heroicons/react/24/outline'

import Dashboard from '../../components/layouts/dashboard'
import Loader from '../../components/loader'
import Notification from '../../components/notification'
import { useUser } from '../../lib/hooks'

function jsonBody(res) {
  return res.json()
}

// previewRegex takes a regex string and an optional list of sample group names,
// then returns the subset that matches. Used for live "Matches:" preview in the form.
// pure function exportable to tests (no React dependencies).
export function previewRegex(regex, sampleGroupNames = ['team-platform', 'ops-general', 'admin-dev']) {
  if (!regex) return []
  try {
    const re = new RegExp(regex)
    return sampleGroupNames.filter(name => re.test(name))
  } catch {
    return []
  }
}

// applyTemplatePreview safely shows the raw template string without evaluating $N references
// (to avoid unsafe eval). Returns empty string for invalid templates.
// pure function exportable to tests (no React dependencies).
export function applyTemplatePreview(template, groupName) {
  if (!template || !groupName) return ''
  try {
    const re = new RegExp(template.replace(/\$([1-9]\d*)/g, '($' + '$1'))
    // Actually just show the raw template with $N highlighted since we can't eval it safely
    return template
  } catch {
    return ''
  }
}

export default function AddGroupsMapping() {
  const router = useRouter()
  const isEdit = !!router.query.id
  
  const [ruleName, setRuleName] = useState('')
  const [sourceGroupRegex, setSourceGroupRegex] = useState('')
  const [destinationType, setDestinationType] = useState('kubernetes')
  const [nameTemplate, setNameTemplate] = useState('')
  const [namespaceTemplate, setNamespaceTemplate] = useState('')
  const [roleTemplate, setRoleTemplate] = useState('')
  
  const [errors, setErrors] = useState({})
  const [submitting, setSubmitting] = useState(false)
  const [notification, setNotification] = useState(null)

  // Memoized live-preview: evaluates sourceGroupRegex against sample group names
  // to show the user which groups would match their regex pattern.
  const sampleGroups = ['team-platform', 'ops-general', 'admin-dev', 'infra-admins']
  const matchedGroups = useMemo(() => previewRegex(sourceGroupRegex), [sourceGroupRegex])

  // Memoized live-preview: shows what the name_template would produce using "team-platform" as input.
  // Replaces $N references with [group-N] placeholders since we can't safely evaluate them client-side.
  const templatePreview = useMemo(() => {
    if (!nameTemplate) return ''
    let result = nameTemplate.replace(/\$([1-9]\d*)/g, (match, num) => {
      const n = parseInt(num, 10)
      // Use sample group parts as a rough preview
      const parts = 'team-platform'.split('-')
      if (n <= parts.length + 1) return `[group-${num}]`
      return ''
    })
    return result
  }, [nameTemplate])

  async function handleSubmit(e) {
    e.preventDefault()
    
    const newErrors = {}

    // Client-side validation: check all required fields and regex validity before submitting.
    if (!ruleName.trim()) newErrors.rule_name = 'Rule name is required'
    if (!sourceGroupRegex.trim()) newErrors.source_group_regex = 'Group matching regex is required'
    else {
      try {
        new RegExp(sourceGroupRegex)
      } catch {
        newErrors.source_group_regex = 'Invalid regular expression'
      }
    }
    if (!nameTemplate.trim()) newErrors.name_template = 'Destination name template is required'
    
    // Role template required for kubernetes
    if (destinationType === 'kubernetes' && !roleTemplate.trim()) {
      newErrors.role_template = 'Role template is required for Kubernetes destinations'
    }

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
        role_template: destinationType === 'kubernetes' ? roleTemplate : null,
      }

      // Build the request body, conditionally including optional fields.
      // For kubernetes: namespace_template is sent only if non-empty.
      // For ssh: namespace_template is omitted entirely (not applicable).
      if (namespaceTemplate && destinationType === 'kubernetes') {
        body.namespace_template = namespaceTemplate
      }

      const url = isEdit ? `/api/mapping-rules/${router.query.id}` : '/api/mapping-rules'
      const method = isEdit ? 'PUT' : 'POST'

      const res = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })

      if (!res.ok) {
        const errBody = await res.json().catch(() => null)
        const message = errBody?.message || 'Failed to save mapping rule'
        setErrors({ submit: Array.isArray(message) ? message.join(', ') : message })
        return
      }

      setNotification({ type: 'success', message: isEdit ? 'Mapping rule updated successfully' : 'Mapping rule created successfully' })
      
      // Navigate back after a brief delay
      setTimeout(() => router.push('/mapping-rules'), 500)
    } catch (e) {
      setErrors({ submit: e.message || 'Failed to save mapping rule' })
    }

    setSubmitting(false)
  }

  return (
    <div className='mb-10'>
      <Head>
        <title>{isEdit ? 'Edit Rule' : 'Add Rule Mapping'} - Infra</title>
      </Head>

      {/* Notification */}
      {notification && (
        <Notification show={true} text={notification.message} setShow={() => setNotification(null)} setClearNotification={() => {}} />
      )}

      <header className='my-6'>
        <Link href='/mapping-rules' className='mb-2 inline-flex items-center text-sm text-gray-500 hover:text-gray-700'>
          <ArrowLeftIcon className='mr-1 h-4 w-4' /> Back to Group Mappings
        </Link>
        <h1 className='py-1 font-display text-xl font-medium'>{isEdit ? 'Edit Rule' : 'Add Rule Mapping'}</h1>
      </header>

      {/* Form */}
      <form onSubmit={handleSubmit} className='space-y-6'>
        {/* Submit error */}
        {errors.submit && (
          <div className='rounded-md bg-red-50 p-3 text-sm text-red-700'>{errors.submit}</div>
        )}

        {/* Rule Name */}
        <div className='mb-4 flex flex-col'>
          <label htmlFor='rule_name' className='text-xs font-medium text-gray-600'>Rule Name</label>
          <input
            id='rule_name'
            name='rule_name'
            required
            type='text'
            value={ruleName}
            onChange={e => setRuleName(e.target.value)}
            placeholder='e.g., team-platform-access'
            className={`mt-1 block w-full rounded-md border ${errors.rule_name ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm`}
          />
          {errors.rule_name && <p className='mt-1 text-xs text-red-500'>{errors.rule_name}</p>}
        </div>

        {/* Destination Type */}
        <div className='mb-4 flex flex-col'>
          <label htmlFor='destination_type' className='text-xs font-medium text-gray-600'>Destination Type</label>
          <select
            id='destination_type'
            name='destination_type'
            value={destinationType}
            onChange={e => setDestinationType(e.target.value)}
            className='mt-1 block w-full rounded-md border border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm'
          >
            <option value='kubernetes'>Kubernetes</option>
            <option value='ssh'>SSH</option>
          </select>
        </div>

        {/* Group Matching Regex */}
        <div className='mb-4 flex flex-col'>
          <label htmlFor='source_group_regex' className='text-xs font-medium text-gray-600'>Group Matching Regex</label>
          <input
            id='source_group_regex'
            name='source_group_regex'
            required
            type='search'
            value={sourceGroupRegex}
            onChange={e => setSourceGroupRegex(e.target.value)}
            placeholder='e.g., ^team-(.*)$'
            className={`mt-1 block w-full rounded-md border ${errors.source_group_regex ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm`}
          />
          {errors.source_group_regex && <p className='mt-1 text-xs text-red-500'>{errors.source_group_regex}</p>}
          
          {/* Regex Preview */}
          {matchedGroups.length > 0 && (
            <div className='mt-2 rounded-md bg-green-50 p-3'>
              <span className='text-xs font-medium text-green-700'>Matches:</span>
              <span className='ml-1 text-xs text-gray-600'>{matchedGroups.join(', ')}</span>
            </div>
          )}
        </div>

        {/* Destination Name Template */}
        <div className='mb-4 flex flex-col'>
          <label htmlFor='name_template' className='text-xs font-medium text-gray-600'>Destination Name Template</label>
          <input
            id='name_template'
            name='name_template'
            required
            type='text'
            value={nameTemplate}
            onChange={e => setNameTemplate(e.target.value)}
            placeholder='e.g., cluster-$1-prod'
            className={`mt-1 block w-full rounded-md border ${errors.name_template ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm`}
          />
          <p className='mt-1 text-xs text-gray-400'>Use $N for capture groups from the regex (e.g., cluster-$1-prod)</p>
          {errors.name_template && <p className='mt-1 text-xs text-red-500'>{errors.name_template}</p>}

          {/* Template Preview */}
          {templatePreview && templatePreview !== nameTemplate && (
            <div className='mt-2 rounded-md bg-blue-50 p-3'>
              <span className='text-xs font-medium text-blue-700'>Example output:</span>
              <span className='ml-1 text-sm text-gray-700'>{templatePreview}</span>
            </div>
          )}
        </div>

        {/* Role Template (only for Kubernetes) */}
        {destinationType === 'kubernetes' && (
          <div className='mb-4 flex flex-col'>
            <label htmlFor='role_template' className='text-xs font-medium text-gray-600'>Role Template</label>
            <input
              id='role_template'
              name='role_template'
              required={destinationType === 'kubernetes'}
              type='text'
              value={roleTemplate}
              onChange={e => setRoleTemplate(e.target.value)}
              placeholder='e.g., $1-admin'
              className={`mt-1 block w-full rounded-md border ${errors.role_template ? 'border-red-500' : 'border-gray-300'} shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm`}
            />
            <p className='mt-1 text-xs text-gray-400'>Use $N for capture groups (e.g., cluster-team-platform-prod → team-platform-admin)</p>
            {errors.role_template && <p className='mt-1 text-xs text-red-500'>{errors.role_template}</p>}

            {/* Role Template Preview */}
            {roleTemplate && (
              <div className='mt-2 rounded-md bg-blue-50 p-3'>
                <span className='text-xs font-medium text-blue-700'>Example role:</span>
                <span className='ml-1 text-sm text-gray-700'>{roleTemplate.replace(/\$([1-9]\d*)/g, '[group-$1]')}</span>
              </div>
            )}
          </div>
        )}

        {/* Namespace Template (only for Kubernetes) */}
        {destinationType === 'kubernetes' && (
          <div className='mb-4 flex flex-col'>
            <label htmlFor='namespace_template' className='text-xs font-medium text-gray-600'>Namespace Template</label>
            <input
              id='namespace_template'
              name='namespace_template'
              type='text'
              value={namespaceTemplate}
              onChange={e => setNamespaceTemplate(e.target.value)}
              placeholder='Optional: e.g., $1-ns (leave empty for cluster-wide)'
              className='mt-1 block w-full rounded-md border border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm'
            />
            <p className='mt-1 text-xs text-gray-400'>Optional. If set, creates namespaced grants (e.g., cluster-$1-prod.$2-ns)</p>
          </div>
        )}

        {/* SSH Note */}
        {destinationType === 'ssh' && (
          <div className='mb-4 rounded-md bg-yellow-50 p-3 text-sm text-yellow-700'>
            For SSH destinations, the privilege is always "connect" and role template is not used.
          </div>
        )}

        {/* Submit */}
        <div className='flex items-center justify-end space-x-3 pt-4'>
          <Link href='/mapping-rules' passHref>
            <button type='button' className='rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50 cursor-pointer'>
              Cancel
            </button>
          </Link>
          <button
            type='submit'
            disabled={submitting}
            className={`rounded-md border border-transparent bg-black px-4 py-2 text-sm font-medium text-white shadow-sm ${submitting ? 'cursor-not-allowed opacity-50' : 'hover:bg-gray-800 hover:cursor-pointer'}`}
          >
            {submitting ? (isEdit ? 'Updating...' : 'Creating...') : (isEdit ? 'Update Rule' : 'Create Rule')}
          </button>
        </div>
      </form>
    </div>
  )
}

AddGroupsMapping.layout = function (page) {
  return <Dashboard>{page}</Dashboard>
}

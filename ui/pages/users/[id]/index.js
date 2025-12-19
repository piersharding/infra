import { useEffect, useMemo, useState } from 'react'
import Head from 'next/head'
import Link from 'next/link'
import { useRouter } from 'next/router'

import useSWR from 'swr'
import dayjs from 'dayjs'
import { ChevronLeftIcon, TrashIcon } from '@heroicons/react/24/outline'
import { CommandLineIcon } from '@heroicons/react/24/solid'

import Dashboard from '../../../components/layouts/dashboard'
import Loader from '../../../components/loader'
import RemoveButton from '../../../components/remove-button'
import GrantForm from '../../../components/grant-form'
import { useUser } from '../../../lib/hooks'
import { sortByPrivilege } from '../../../lib/grants'

export default function UserDetail() {
  const router = useRouter()
  const userId = router.query.id
  const [selectedResources, setSelectedResources] = useState([])

  const { user: currentUser, isAdmin, isAdminLoading } = useUser()
  const { data: user } = useSWR(userId ? `/api/users/${userId}` : null)
  const { data: { items: grants } = {}, mutate } = useSWR(
    userId ? `/api/grants?user=${userId}&showInherited=1&limit=1000` : null
  )
  const { data: { items: destinations } = {} } = useSWR(
    '/api/destinations?limit=1000'
  )
  const { data: { items: groups } = {} } = useSWR('/api/groups?limit=1000')

  useEffect(() => {
    mutate(`/api/grants?user=${userId}&limit=1000`)
  }, [userId, mutate])

  const destinationRolesMap = useMemo(() => {
    const map = new Map()
    destinations?.forEach(d => {
      map.set(d.name, d.roles && d.roles.length > 0 ? d.roles : ['connect'])
    })
    return map
  }, [destinations])

  const grantsList = useMemo(() => {
    const entriesMap = new Map()
    grants?.forEach(g => {
      const resource = g.resource
      const base = resource?.split('.')?.[0] || ''
      const kind =
        base === 'infra'
          ? 'infra'
          : destinationRolesMap.has(base)
            ? destinations?.find(d => d.name === base)?.kind || 'other'
            : 'other'

      const current = entriesMap.get(resource) || {
        privileges: new Set(),
        kind,
        hasGroupGrant: false,
        hasUserGrant: false,
        userGrantIds: [],
        groupNames: new Set(),
      }
      current.privileges.add(g.privilege)
      if (g.group) {
        current.hasGroupGrant = true
        const groupName = groups?.find(grp => grp.id === g.group)?.name
        current.groupNames.add(groupName || g.group)
      }
      if (g.user) {
        current.hasUserGrant = true
        current.userGrantIds.push(g.id)
      }
      entriesMap.set(resource, current)
    })

    return Array.from(entriesMap.entries()).map(([resource, value]) => ({
      resource,
      privileges: Array.from(value.privileges),
      kind: value.kind,
      hasGroupGrant: value.hasGroupGrant,
      hasUserGrant: value.hasUserGrant,
      userGrantIds: value.userGrantIds,
      groupNames: Array.from(value.groupNames),
    }))
  }, [grants, destinationRolesMap, destinations, groups])

  if (isAdminLoading || !user) {
    return <Loader />
  }

  return (
    <div className='mb-10'>
      <Head>
        <title>{user?.name} - Infra</title>
      </Head>
      <header className='mt-6 mb-10 space-y-2'>
        <div className='flex items-center space-x-2'>
          <Link href='/users' className='text-gray-500 hover:text-gray-700'>
            <ChevronLeftIcon className='h-4 w-4' />
          </Link>
          <h1 className='py-1 font-display text-xl font-medium'>
            {user?.name}
          </h1>
        </div>
        <div className='text-xs text-gray-500'>
          Created {user?.created ? dayjs(user.created).fromNow() : '-'} • Last
          seen {user?.lastSeenAt ? dayjs(user.lastSeenAt).fromNow() : '-'}
        </div>
      </header>

      <section className='mb-6 rounded-lg border border-gray-200/75 bg-white p-4'>
        <div className='flex items-center justify-between'>
          <h3 className='text-sm font-medium text-gray-800'>Grants</h3>
          {isAdmin && userId !== currentUser?.id && (
            <RemoveButton
              onClick={async () => {
                await fetch(`/api/users/${userId}`, { method: 'DELETE' })
                router.push('/users')
              }}
            >
              <TrashIcon className='mr-2 h-4 w-4' /> Remove user
            </RemoveButton>
          )}
        </div>

        {['kubernetes', 'ssh', 'infra', 'other'].map(kind => {
          const rows = grantsList
            .filter(g => g.kind === kind)
            .sort((a, b) => a.resource.localeCompare(b.resource))
          if (!rows.length) return null
          return (
            <div key={kind} className='mt-4'>
              <div className='mb-2 flex items-center space-x-2 text-xs font-semibold text-gray-700'>
                {kind === 'kubernetes' ? (
                  <img
                    alt='kubernetes icon'
                    className='h-4'
                    src={`/kubernetes.svg`}
                  />
                ) : kind === 'ssh' ? (
                  <CommandLineIcon className='h-4 w-4 text-gray-800' />
                ) : kind === 'infra' ? (
                  <img alt='infra icon' className='h-4' src={`/icon.svg`} />
                ) : (
                  <span className='h-4 w-4' />
                )}
                <span className='uppercase'>{kind}</span>
              </div>
              <div className='space-y-2'>
                {rows.map(
                  ({
                    resource,
                    privileges,
                    hasGroupGrant,
                    hasUserGrant,
                    userGrantIds,
                    groupNames,
                  }) => (
                    <div
                      key={`${kind}-${resource}`}
                      className='flex items-center justify-between rounded-md border border-gray-200 px-3 py-2'
                    >
                      <div className='flex flex-col'>
                        <span className='text-xs font-medium text-gray-800'>
                          {resource || 'cluster'}
                        </span>
                        <span className='text-2xs text-gray-500'>
                          {privileges.sort(sortByPrivilege).join(', ')}
                          {hasGroupGrant && (
                            <span className='ml-1 italic text-gray-500'>
                              (inherited from {groupNames.join(', ')})
                            </span>
                          )}
                        </span>
                      </div>
                      {isAdmin && hasUserGrant && (
                        <RemoveButton
                          onClick={async () => {
                            await fetch('/api/grants', {
                              method: 'PATCH',
                              body: JSON.stringify({
                                grantsToRemove: userGrantIds.map(id => ({
                                  id,
                                })),
                              }),
                            })
                            mutate()
                          }}
                        >
                          <TrashIcon className='mr-2 h-4 w-4' /> Remove
                        </RemoveButton>
                      )}
                    </div>
                  )
                )}
              </div>
            </div>
          )
        })}
      </section>

      {isAdmin && (
        <section className='rounded-lg border border-gray-200/75 bg-white p-4'>
          <h3 className='text-sm font-medium text-gray-800'>Add grant</h3>
          <div className='mt-3 flex flex-col space-y-3'>
            <GrantForm
              roles={
                destinationRolesMap.get(selectedResources[0]) || ['connect']
              }
              selectedResources={selectedResources}
              multiselect={false}
              grants={grants}
              onSubmit={async ({ privilege }) => {
                const resource = selectedResources[0] || ''

                await fetch('/api/grants', {
                  method: 'PATCH',
                  body: JSON.stringify({
                    grantsToAdd: [
                      {
                        user: parseInt(userId),
                        privilege,
                        resource,
                      },
                    ],
                  }),
                })

                mutate()
                setSelectedResources([])
              }}
            />
            <div className='flex flex-wrap gap-2 text-2xs text-gray-500'>
              {(destinations || []).map(d => (
                <button
                  key={d.id}
                  type='button'
                  onClick={() => setSelectedResources([d.name])}
                  className={`rounded-full border px-3 py-1 text-xs ${
                    selectedResources.includes(d.name)
                      ? 'border-blue-500 text-blue-600'
                      : 'border-gray-200 text-gray-600'
                  }`}
                >
                  {d.name}
                </button>
              ))}
            </div>
          </div>
        </section>
      )}
    </div>
  )
}

UserDetail.layout = page => <Dashboard>{page}</Dashboard>

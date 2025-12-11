import { useEffect, useState, useRef } from 'react'

import useSWR from 'swr'
import { Combobox } from '@headlessui/react'
import { PlusIcon, CheckIcon } from '@heroicons/react/24/outline'

import { sortByRole } from '../lib/grants'
import { useDebouncedSearch } from '../lib/hooks'

import RoleSelect from './role-select'

export default function GrantForm({
  grants,
  roles,
  selectedResources,
  multiselect = true,
  onSubmit = () => {},
}) {
  const [role, setRole] = useState('')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(null)
  const [options, setOptions] = useState([])

  const button = useRef()

  const debouncedQuery = useDebouncedSearch(query, 300)

  const { data: { items: users } = { items: [] }, isLoading: usersLoading } =
    useSWR(
      debouncedQuery.length >= 2
        ? `/api/users?name=${encodeURIComponent(debouncedQuery)}&limit=50`
        : null
    )
  const { data: { items: groups } = { items: [] }, isLoading: groupsLoading } =
    useSWR(
      debouncedQuery.length >= 2
        ? `/api/groups?name=${encodeURIComponent(debouncedQuery)}&limit=50`
        : null
    )

  useEffect(() => {
    setRole(sortByRole(roles)?.[0])
  }, [roles])

  useEffect(() => {
    if (debouncedQuery.length < 2) {
      setOptions([])
      return
    }

    if (users && groups) {
      const optionsList = [
        ...(groups?.map(g => ({ ...g, group: true })) || []),
        ...(users?.map(u => ({ ...u, user: true })) || []),
      ]
      const filteredOptions = multiselect
        ? optionsList
        : optionsList?.filter(
            item =>
              !grants?.find(g => g.user === item.id || g.group === item.id)
          )

      setOptions(filteredOptions)
    }
  }, [users, groups, grants, debouncedQuery, multiselect])

  return (
    <form
      className='my-2 flex flex-row space-x-3'
      onSubmit={e => {
        e.preventDefault()
        onSubmit({
          user: selected.user ? selected.id : undefined,
          group: selected.group ? selected.id : undefined,
          privilege: role,
          selectedResources,
        })

        setRole(sortByRole(roles)?.[0])
        setSelected(null)
      }}
    >
      <div className='flex flex-1 items-center'>
        <Combobox
          as='div'
          className='relative flex-1'
          value={selected?.name || ''}
          onChange={setSelected}
        >
          <Combobox.Input
            className={`block w-full rounded-md border-gray-300 text-xs shadow-sm focus:border-blue-500 focus:ring-blue-500`}
            placeholder='Enter group or user (min 2 chars)'
            onChange={e => {
              setQuery(e.target.value)
              if (e.target.value.length === 0) {
                setSelected(null)
              }
            }}
            onFocus={() => {
              if (!selected && query.length >= 2) {
                button.current?.click()
              }
            }}
            type='search'
          />
          {query.length > 0 && query.length < 2 && (
            <div className='absolute z-10 mt-2 w-56 rounded-md bg-white p-3 shadow-lg ring-1 ring-black ring-opacity-5'>
              <div className='text-xs text-gray-500'>
                Type at least 2 characters to search
              </div>
            </div>
          )}
          {(groupsLoading || usersLoading) && query.length >= 2 && (
            <div className='absolute z-10 mt-2 w-56 rounded-md bg-white p-3 shadow-lg ring-1 ring-black ring-opacity-5'>
              <div className='flex items-center text-xs text-gray-500'>
                <div className='animate-spin rounded-full h-3 w-3 border-b-2 border-blue-500 mr-2'></div>
                Searching...
              </div>
            </div>
          )}
          {!groupsLoading &&
            !usersLoading &&
            options?.length === 0 &&
            query.length >= 2 && (
              <div className='absolute z-10 mt-2 w-56 rounded-md bg-white p-3 shadow-lg ring-1 ring-black ring-opacity-5'>
                <div className='text-xs text-gray-500'>
                  No users or groups found
                </div>
              </div>
            )}
          {!groupsLoading && !usersLoading && options?.length > 0 && (
            <Combobox.Options className='absolute z-10 mt-2 max-h-60 w-56 origin-top-right divide-y divide-gray-100 overflow-auto rounded-md bg-white text-xs shadow-lg shadow-gray-300/20 ring-1 ring-black ring-opacity-5 focus:outline-none'>
              {options?.map(f => (
                <Combobox.Option
                  key={f.id}
                  value={f}
                  className={({ active }) =>
                    `relative cursor-default select-none py-[7px] px-3 ${
                      active ? 'bg-gray-50' : ''
                    }`
                  }
                >
                  <div className='flex flex-row'>
                    <div className='flex min-w-0 flex-1 flex-col'>
                      <div className='flex justify-between py-0.5 font-medium'>
                        <span className='truncate' title={f.name}>
                          {f.name}
                        </span>
                        {selected && selected.id === f.id && (
                          <CheckIcon
                            data-testid='selected-icon'
                            className='h-3 w-3 stroke-1 text-gray-600'
                            aria-hidden='true'
                          />
                        )}
                      </div>
                      <div className='text-3xs text-gray-500'>
                        {f.user ? 'User' : f.group ? 'Group' : ''}
                      </div>
                    </div>
                  </div>
                </Combobox.Option>
              ))}
            </Combobox.Options>
          )}
          <Combobox.Button className='hidden' ref={button} />
        </Combobox>
      </div>
      {roles?.length > 1 && (
        <div className='relative'>
          <RoleSelect
            onChange={setRole}
            role={role}
            roles={sortByRole(roles)}
          />
        </div>
      )}
      <div className='relative'>
        <button
          disabled={!selected}
          type='submit'
          className='inline-flex items-center rounded-md border border-transparent bg-black px-4 py-[7px] text-xs font-medium text-white shadow-sm hover:cursor-pointer hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-30'
        >
          <PlusIcon className='mr-1 h-3 w-3' />
          Add
        </button>
      </div>
    </form>
  )
}

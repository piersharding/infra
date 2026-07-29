import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import GroupsMapping from '../../../pages/mapping-rules/index'

// Mock dependencies at module level (Jest hoists jest.mock() calls).
jest.mock('next/router', () => ({
  useRouter: jest.fn(),
}))

jest.mock('../../../lib/hooks', () => ({
  useUser: () => ({ isAdmin: true, isAdminLoading: false }),
}))

// Mock fetch at module level — returns rule data for GET /api/mapping-rules/:id,
// and empty response for other calls (e.g., POST/DELETE).
global.fetch = jest.fn(url => {
  if (/\/api\/mapping-rules\/.+/.test(url)) {
    return Promise.resolve({
      ok: true,
      json: () =>
        Promise.resolve({
          id: '1',
          rule_name: 'team-access',
          source_group_regex: '^team-(.*)$',
          destination_type: 'kubernetes',
          name_template: 'cluster-$1',
          role_template: '$2-admin',
        }),
    })
  }
  return Promise.resolve({ ok: true, json: () => Promise.resolve({}) })
})

describe('Groups Mapping — edit mode dialog behavior', () => {
  let user = null

  beforeEach(() => {
    jest.clearAllMocks()
    global.fetch.mockClear()
    user = userEvent.setup({ advanceTimers: Date.now })
  })

  it('renders an Edit link on each mapping rule row', async () => {
    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    let callCount = 0
    const mockSwr = jest.fn().mockImplementation(_key => {
      if (callCount === 0) {
        callCount++
        return {
          data: {
            items: [
              {
                id: '1',
                rule_name: 'team-access',
                source_group_regex: '^team-(.*)$',
                destination_type: 'kubernetes',
              },
            ],
            totalCount: 1,
            totalPages: 1,
          },
        }
      }
      return { data: null }
    })

    jest.spyOn(require('swr'), 'default').mockImplementation(mockSwr)

    render(<GroupsMapping />)

    // Edit link should be present on the row (hover-visible).
    const editLinks = screen.getAllByText(/Edit/)
    expect(editLinks.length).toBeGreaterThan(0)
  })

  it('pre-fills form fields when clicking Edit', async () => {
    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    let callCount = 0
    const mockSwr = jest.fn().mockImplementation(_key => {
      if (callCount === 0) {
        callCount++
        return {
          data: {
            items: [
              {
                id: '1',
                rule_name: 'team-access',
                source_group_regex: '^team-(.*)$',
                destination_type: 'kubernetes',
                name_template: 'cluster-$1',
                role_template: '$2-admin',
              },
            ],
            totalCount: 1,
            totalPages: 1,
          },
        }
      }
      return { data: null }
    })

    jest.spyOn(require('swr'), 'default').mockImplementation(mockSwr)

    render(<GroupsMapping />)

    // Click the Edit link on the row.
    const editLinks = screen.getAllByText(/Edit/)
    await user.click(editLinks[0])

    // Wait for fetch to resolve and useEffect to populate fields.
    await waitFor(() => {
      const inputs = document.querySelectorAll('#mr-name')
      expect([...inputs].some(i => i.value === 'team-access')).toBe(true)
    })

    // Verify fields are populated from the rule.
    const mrInputs = document.querySelectorAll('#mr-name')
    expect([...mrInputs].some(i => i.value === 'team-access')).toBe(true)

    // Verify button text changed to "Save Rule".
    expect(screen.getByText(/Save Rule/)).toBeInTheDocument()
  })

  it('clears fields when clicking Add after Edit', async () => {
    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    let callCount = 0
    const mockSwr = jest.fn().mockImplementation(_key => {
      if (callCount === 0) {
        callCount++
        return {
          data: {
            items: [
              {
                id: '1',
                rule_name: 'team-access',
                source_group_regex: '^team-(.*)$',
                destination_type: 'kubernetes',
                name_template: 'cluster-$1',
                role_template: '$2-admin',
              },
            ],
            totalCount: 1,
            totalPages: 1,
          },
        }
      }
      return { data: null }
    })

    jest.spyOn(require('swr'), 'default').mockImplementation(mockSwr)

    render(<GroupsMapping />)

    // Click Edit to open dialog with pre-filled fields.
    const editLinks = screen.getAllByText(/Edit/)
    await user.click(editLinks[0])

    // Wait for fetch + useEffect to populate fields.
    await waitFor(() => {
      const inputs = document.querySelectorAll('#mr-name')
      expect([...inputs].some(i => i.value === 'team-access')).toBe(true)
    })

    // Close the dialog without saving (click ✕ close button in header).
    // Note: closing unmounts the Dialog, so fields aren't cleared by useEffect.
    const closeButtons = document.querySelectorAll('button[type="button"]')
    const closeButton = [...closeButtons].find(b =>
      b.textContent?.includes('\u2715')
    )
    await user.click(closeButton)

    // Click Add Rule to open a fresh dialog.
    const addButton = screen.getByText(/Add Rule/)
    await user.click(addButton)

    // Verify fields are empty (not pre-filled from last edit).
    await waitFor(() => {
      const addInputs = document.querySelectorAll('#mr-name')
      expect([...addInputs].every(i => i.value === '')).toBe(true)
    })

    // Verify button text is "Create Rule" for fresh add.
    expect(screen.getByText(/Create Rule/)).toBeInTheDocument()
  })

  it('shows "Save Rule" button when in edit mode', async () => {
    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    let callCount = 0
    const mockSwr = jest.fn().mockImplementation(_key => {
      if (callCount === 0) {
        callCount++
        return {
          data: {
            items: [
              {
                id: '1',
                rule_name: 'team-access',
                source_group_regex: '^team-(.*)$',
                destination_type: 'kubernetes',
                name_template: 'cluster-$1',
                role_template: '$2-admin',
              },
            ],
            totalCount: 1,
            totalPages: 1,
          },
        }
      }
      return { data: null }
    })

    jest.spyOn(require('swr'), 'default').mockImplementation(mockSwr)

    render(<GroupsMapping />)

    // Click Edit to open dialog in edit mode.
    const editLinks = screen.getAllByText(/Edit/)
    await user.click(editLinks[0])

    expect(screen.getByText(/Save Rule/)).toBeInTheDocument()
  })

  it('shows "Create Rule" button when opening fresh add dialog', async () => {
    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    let callCount = 0
    const mockSwr = jest.fn().mockImplementation(_key => {
      if (callCount === 0) {
        callCount++
        return {
          data: {
            items: [
              {
                id: '1',
                rule_name: 'team-access',
                source_group_regex: '^team-(.*)$',
                destination_type: 'kubernetes',
                name_template: 'cluster-$1',
                role_template: '$2-admin',
              },
            ],
            totalCount: 1,
            totalPages: 1,
          },
        }
      }
      return { data: null }
    })

    jest.spyOn(require('swr'), 'default').mockImplementation(mockSwr)

    render(<GroupsMapping />)

    // Click Add Rule to open a fresh dialog.
    const addButton = screen.getByText(/Add Rule/)
    await user.click(addButton)

    expect(screen.getByText(/Create Rule/)).toBeInTheDocument()
  })
})

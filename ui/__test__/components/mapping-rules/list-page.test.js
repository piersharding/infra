import { render, screen } from '@testing-library/react'
import GroupsMapping from '../../../pages/mapping-rules/index'

// Mock dependencies at module level (Jest hoists jest.mock() calls).
jest.mock('next/router', () => ({
  useRouter: jest.fn(),
}))

jest.mock('../../../lib/hooks', () => ({
  useUser: () => ({ isAdmin: true, isAdminLoading: false }),
}))

// SWR mock — set up in beforeEach to avoid Jest hoisting TDZ issues.

describe('Groups Mapping — list page', () => {
  let swrSpy = null

  beforeEach(() => {
    jest.clearAllMocks()
    // Reset SWR mock to return empty data by default.
    const fn = jest.fn().mockReturnValue({ data: null })
    swrSpy = jest.spyOn(require('swr'), 'default').mockImplementation(fn)
  })

  it('renders a Remove button on each mapping rule row', async () => {
    let callCount = 0
    const mockFn = jest.fn(_key => {
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
              {
                id: '2',
                rule_name: 'ssh-rules',
                source_group_regex: '^ops-(.*)$',
                destination_type: 'ssh',
              },
            ],
            totalCount: 2,
            totalPages: 1,
          },
        }
      }
      // Second call (/api/groups) returns empty.
      return { data: null }
    })

    swrSpy.mockImplementation(mockFn)

    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    render(<GroupsMapping />)

    // Verify data cells are rendered.
    expect(screen.getByText('team-access')).toBeInTheDocument()
    expect(screen.getByText('^team-(.*)$')).toBeInTheDocument()
    expect(screen.getByText('Kubernetes')).toBeInTheDocument()

    // Verify Remove buttons exist for each row.
    const removeButtons = screen.getAllByText(/Remove/)
    expect(removeButtons).toHaveLength(2)
  })

  it('shows empty message when no mappings exist', async () => {
    const useRouter = require('next/router').useRouter
    useRouter.mockReturnValue({
      query: {},
      push: jest.fn(),
      replace: jest.fn(),
    })

    render(<GroupsMapping />)

    expect(screen.getByText(/No mapping rules/)).toBeInTheDocument()
  })
})

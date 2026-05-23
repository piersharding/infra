import { render, screen } from '@testing-library/react'
import GroupsMapping from '../../pages/mapping-rules/index'

// Mock SWR to prevent actual API calls during tests
jest.mock('swr', () => ({
  __esModule: true,
  default: jest.fn(() => ({})),
  mutate: jest.fn(),
}))

// Mock useUser so isAdmin is always true (test user has admin access)
jest.mock('../../lib/hooks', () => ({
  useUser: () => ({ isAdmin: true, isAdminLoading: false }),
}))

// List page tests: verify table rendering and empty state.
describe('Groups Mapping — list page', () => {
  it('renders table cells with mock data rows', async () => {
    const swr = require('swr')
    swr.default.mockReturnValue({
      data: {
        items: [
          { id: '1', rule_name: 'team-access', source_group_regex: '^team-(.*)$', destination_type: 'kubernetes' },
          { id: '2', rule_name: 'ssh-rules', source_group_regex: '^ops-(.*)$', destination_type: 'ssh' },
        ],
      },
    })

    render(<GroupsMapping />)

    expect(screen.getByText('team-access')).toBeInTheDocument()
    expect(screen.getByText('^team-(.*)$')).toBeInTheDocument()
    expect(screen.getByText('Kubernetes')).toBeInTheDocument()
  })

  it('shows empty message when no mappings exist', async () => {
    const swr = require('swr')
    swr.default.mockReturnValue({
      data: { items: [], totalCount: 0 },
    })

    render(<GroupsMapping />)

    expect(screen.getByText(/No mapping rules/)).toBeInTheDocument()
  })
})

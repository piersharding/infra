import { render, screen, fireEvent } from '@testing-library/react'
import AddGroupsMapping from '../../pages/groups-mapping/add'

// Mock fetch and router
global.fetch = jest.fn(() => Promise.resolve({ ok: true, json: () => Promise.resolve({}) }))
jest.mock('next/router', () => ({
  useRouter: () => ({ query: {}, push: jest.fn(), replace: jest.fn() }),
}))

// Mock useUser hook
jest.mock('../../lib/hooks', () => ({
  useUser: () => ({ isAdmin: true, isAdminLoading: false }),
}))

describe('Groups Mapping — create dialog form validation', () => {
  beforeEach(() => {
    global.fetch.mockClear()
  })

  it('renders required fields for kubernetes destination type', async () => {
    render(<AddGroupsMapping />)

    expect(screen.getByLabelText(/Rule Name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/Source Group Regex/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/Destination Type/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/Name Template/i)).toBeInTheDocument()
    // Role template should be visible for kubernetes (default)
    expect(screen.getByLabelText(/Role Template/i)).toBeInTheDocument()
  })

  it('shows role template only when destination type is kubernetes', async () => {
    render(<AddGroupsMapping />)

    const select = screen.getByLabelText(/Destination Type/i)
    fireEvent.change(select, { target: { value: 'ssh' } })

    // Role template should be hidden for SSH
    expect(screen.queryByLabelText(/Role Template/i)).not.toBeInTheDocument()
  })

  it('shows namespace template only when destination type is kubernetes', async () => {
    render(<AddGroupsMapping />)

    const select = screen.getByLabelText(/Destination Type/i)
    fireEvent.change(select, { target: { value: 'ssh' } })

    // Namespace template should be hidden for SSH
    expect(screen.queryByLabelText(/Namespace Template/i)).not.toBeInTheDocument()
  })

  it('rejects empty required fields on submit', async () => {
    render(<AddGroupsMapping />)

    const submitBtn = screen.getByRole('button', { name: /Create Rule|Creating/i })
    fireEvent.click(submitBtn)

    // Should show validation errors for missing required fields
    expect(screen.queryByText(/Rule name is required/)).toBeInTheDocument()
    expect(screen.queryByText(/Source group regex is required/)).toBeInTheDocument()
    expect(screen.queryByText(/Name template is required/)).toBeInTheDocument()
  })

  it('shows role template as visible and required for kubernetes destination', async () => {
    render(<AddGroupsMapping />)

    // Default is kubernetes, so role_template should be visible
    const roleLabel = screen.getByLabelText(/Role Template/i)
    expect(roleLabel).toBeInTheDocument()
  })
})

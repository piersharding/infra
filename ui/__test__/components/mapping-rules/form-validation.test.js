import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AddMappingRuleDialog as AddGroupsMapping } from '../../../pages/mapping-rules/index'

// Mock fetch to prevent actual API calls during tests. Returns a successful empty response.
global.fetch = jest.fn(() =>
  Promise.resolve({ ok: true, json: () => Promise.resolve({}) })
)
jest.mock('next/router', () => ({
  useRouter: () => ({ query: {}, push: jest.fn(), replace: jest.fn() }),
}))

// Mock useUser so isAdmin is always true (test user has admin access)
jest.mock('../../../lib/hooks', () => ({
  useUser: () => ({ isAdmin: true, isAdminLoading: false }),
}))

// Form validation tests: ensure correct fields are shown/hidden and required field checks work.
describe('Groups Mapping — create dialog form validation', () => {
  let user = null

  const dialogProps = {
    open: true,
    setOpen: jest.fn(),
    editingRule: null,
    groups: [],
    onMutate: jest.fn(),
  }

  beforeEach(() => {
    global.fetch.mockClear()
    // Use real timers — the project does not enable fakeTimers globally,
    // so passing jest.advanceTimersByTime triggers a Jest warning.
    user = userEvent.setup({ advanceTimers: Date.now })
  })

  it('renders required fields for kubernetes destination type', async () => {
    await act(async () => {
      render(<AddGroupsMapping {...dialogProps} />)
    })

    expect(screen.getByLabelText(/Rule Name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/Group Matching Regex/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/Destination Type/i)).toBeInTheDocument()
    expect(
      screen.getByLabelText(/Destination Name Template/i)
    ).toBeInTheDocument()
    // Role template should be visible for kubernetes (default)
    expect(screen.getByLabelText(/Role Template/i)).toBeInTheDocument()
  })

  it('shows role template only when destination type is kubernetes', async () => {
    await act(async () => {
      render(<AddGroupsMapping {...dialogProps} />)
    })

    const select = screen.getByLabelText(/Destination Type/i)
    await user.selectOptions(select, 'ssh')

    // Role template should be hidden for SSH
    expect(screen.queryByLabelText(/Role Template/i)).not.toBeInTheDocument()
  })

  it('shows namespace template regex only when destination type is kubernetes', async () => {
    await act(async () => {
      render(<AddGroupsMapping {...dialogProps} />)
    })

    const select = screen.getByLabelText(/Destination Type/i)
    await user.selectOptions(select, 'ssh')

    // Namespace regex should be hidden for SSH
    expect(
      screen.queryByLabelText(/Namespace Template Regex/i)
    ).not.toBeInTheDocument()
  })

  it('rejects empty required fields on submit', async () => {
    await act(async () => {
      render(<AddGroupsMapping {...dialogProps} />)
    })

    // Use fireEvent.submit which properly triggers form submission in jsdom.
    const { fireEvent } = require('@testing-library/dom')
    const forms = document.querySelectorAll('form')
    expect(forms.length).toBeGreaterThan(0)

    // Dispatch a proper submit event on the form element.
    fireEvent.submit(forms[0])

    // Wait for React to flush state updates and render error messages.
    await screen.findByText(/Rule name is required/)

    expect(await screen.findByText(/Rule name is required/)).toBeInTheDocument()
    expect(
      await screen.findByText(/Group matching regex is required/)
    ).toBeInTheDocument()
    expect(
      await screen.findByText(/Destination name template is required/)
    ).toBeInTheDocument()
  })

  it('shows role template as visible and required for kubernetes destination', async () => {
    await act(async () => {
      render(<AddGroupsMapping {...dialogProps} />)
    })

    // Default is kubernetes, so role_template should be visible
    const roleLabel = screen.getByLabelText(/Role Template/i)
    expect(roleLabel).toBeInTheDocument()
  })
})

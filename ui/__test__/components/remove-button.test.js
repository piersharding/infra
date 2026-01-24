import { render, screen, fireEvent } from '@testing-library/react'

import RemoveButton from '../../components/remove-button'

// NOTE: Do not mock `@headlessui/react` here.
// Transitions are controlled via our wrapper (`components/ui/transition`) in the app code.
// Mocking Headless UI at the package level can break `Dialog` semantics in tests.

global.IntersectionObserver = jest.fn(() => ({
  observe: () => {},
  unobserve: () => {},
  disconnect: () => {},
}))

describe('Remove Button Component', () => {
  it('should render', () => {
    expect(() => render(<RemoveButton onRemove={() => {}} />)).not.toThrow()
  })

  it('should trigger the delete modal when button is clicked', () => {
    const modalTitle = 'test remove button delete modal title'
    const modalMessage = 'test remove button delete modal message'

    render(
      <RemoveButton
        onRemove={() => {}}
        modalTitle={modalTitle}
        modalMessage={modalMessage}
      />
    )

    expect(screen.queryByText(modalTitle)).not.toBeInTheDocument()
    expect(screen.queryByText(modalMessage)).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('remove-button'))

    expect(screen.queryByText(modalTitle)).toBeInTheDocument()
    expect(screen.queryByText(modalMessage)).toBeInTheDocument()
  })
})

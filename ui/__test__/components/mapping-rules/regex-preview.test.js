// Tests for the exported pure functions: previewRegex and applyTemplatePreview.
// These test the core logic without React dependencies.
import {
  previewRegex,
  previewTemplate as applyTemplatePreview,
} from '../../../lib/mappingRules'

describe('Groups Mapping — regex preview', () => {
  it('returns matching groups for a valid regex', () => {
    const result = previewRegex('^team-(.*)$', ['team-platform', 'ops-general'])
    expect(result).toContain('team-platform')
    expect(result).not.toContain('ops-general')
  })

  it('returns empty array for invalid regex', () => {
    const result = previewRegex('[invalid(', ['anything'])
    expect(result).toEqual([])
  })

  it('matches all groups when regex is .*', () => {
    const result = previewRegex('.*', ['a', 'b', 'c'])
    expect(result).toHaveLength(3)
  })

  it('returns empty array for no match', () => {
    const result = previewRegex('^admin-(.*)$', [
      'team-platform',
      'ops-general',
    ])
    expect(result).toEqual([])
  })

  it('handles multiple capture groups in regex', () => {
    const result = previewRegex('^(.+)-(.+)$', [
      'team-platform-dev',
      'ops-admin-production',
    ])
    expect(result).toHaveLength(2)
  })
})

// Template preview tests: verify that applyTemplatePreview handles edge cases safely.
describe('Groups Mapping — template preview', () => {
  it('returns em-dash for missing template', () => {
    const result = applyTemplatePreview('', 'team-platform')
    expect(result).toBe('\u2014')
  })

  it('returns the raw template when no $N references exist', () => {
    const result = applyTemplatePreview('static-name', 'team-platform')
    expect(result).toBe('static-name')
  })

  it('replaces $N with [group-N] placeholders', () => {
    const result = applyTemplatePreview('$1-admin', 'team-platform')
    // Replaces $1 with [group-1] placeholder
    expect(result).toBe('[group-1]-admin')
  })

  it('passes through ${...} unchanged (no $N patterns to replace)', () => {
    const result = applyTemplatePreview('${invalid}', 'anything')
    // ${invalid} is not a valid $N reference, so it passes through
    expect(result).toBe('${invalid}')
  })
})

// Mapping rule utilities for client-side previews.
// Exported pure functions with no React dependencies — safe to import in tests.

/**
 * previewRegex takes a regex string and an optional list of sample group names,
 * then returns the subset that matches. Used for live "Matches:" preview in forms.
 */
export function previewRegex(
  regex,
  sampleGroupNames = ['team-platform', 'ops-general', 'admin-dev']
) {
  if (!regex) return []
  try {
    const re = new RegExp(regex)
    return sampleGroupNames.filter(name => re.test(name))
  } catch {
    return []
  }
}

/**
 * previewTemplate takes a template string and replaces $N references with [group-N] placeholders.
 * Used to show users what their templates will produce without evaluating them client-side.
 */
export function previewTemplate(template, sampleGroup = 'team-platform') {
  if (!template || !sampleGroup) return '\u2014'

  const parts = sampleGroup.split('-')
  let result = template.replace(/\$([1-9]\d*)/g, (_, numStr) => {
    const n = parseInt(numStr, 10)
    // Use sample group parts as a rough preview — +2 allows for common capture patterns.
    // Supports multi-digit references ($10, $25+) matching the backend's applyTemplate.
    if (n <= parts.length + 2 && n > 0) return `[group-${numStr}]`
    return ''
  })

  return result
}

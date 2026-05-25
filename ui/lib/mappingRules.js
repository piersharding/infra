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
 * previewTemplate takes a template string and replaces $N references with the actual
 * Nth capture group from matching sourceGroupRegex against sampleGroup.
 * When sourceGroupRegex is provided, it uses real regex captures for accurate previews.
 * Used to show users what their templates will produce without evaluating them client-side.
 *
 * @param {string} template - Template string with $N references (e.g., "cluster-$1")
 * @param {string} sampleGroup - Sample group name to test the template against
 * @param {string} [sourceGroupRegex] - Optional source_group_regex from the mapping rule.
 *   When provided, captures are derived from this regex matching the sample group,
 *   giving accurate previews instead of naive placeholders.
 */
export function previewTemplate(
  template,
  sampleGroup = 'team-platform',
  sourceGroupRegex
) {
  if (!template || !sampleGroup) return '\u2014'

  // Extract all $N references from the template (supports multi-digit: $1, $10, etc.)
  const refPattern = /\$([1-9]\d*)/g
  let match
  const refs = []
  while ((match = refPattern.exec(template)) !== null) {
    refs.push(parseInt(match[1], 10))
  }

  if (refs.length === 0) return template

  // If sourceGroupRegex is provided, use it to get actual capture groups.
  if (sourceGroupRegex) {
    try {
      const re = new RegExp(sourceGroupRegex)
      const matches = sampleGroup.match(re)
      if (matches && matches.length > 1) {
        let result = template
        for (const ref of refs) {
          // Replace all occurrences of $ref with the captured group value.
          const replacement =
            ref < matches.length ? matches[ref] : `[group-${ref}]`
          result = result.replace(new RegExp(`\\$${ref}`, 'g'), replacement)
        }
        return result
      }
    } catch {
      // Regex compilation failed — fall through to placeholder below.
    }
  }

  // Fallback: show placeholders for capture group references.
  let result = template.replace(/\$([1-9]\d*)/g, (_, numStr) => {
    const n = parseInt(numStr, 10)
    return n > 0 ? `[group-${numStr}]` : ''
  })

  return result
}

const fetch = global.fetch

const base = '0.19.1'

// Patch the global fetch to automatically inject the 'Infra-Version' header
// on all cross-domain requests. The header is applied in a merge step AFTER
// spreading info, ensuring caller-provided headers (Content-Type, Authorization,
// etc.) take precedence while Infra-Version always gets set for same-origin URLs.
//
// Why two spread layers?
// 1st spread: handles the case where info has no 'headers' key at all — provides
//             a default { headers: {} } so the 2nd layer can safely merge.
// 2nd spread: merges any caller-provided headers on top of Infra-Version,
//             preventing accidental overwrites when callers pass their own headers object.
global.fetch = (resource, info) =>
  fetch(resource, {
    ...(resource.startsWith('/') ? { headers: { 'Infra-Version': base } } : {}),
    ...info,
    headers: {
      ...info?.headers,
      ...(resource.startsWith('/') ? { 'Infra-Version': base } : {}),
    },
  })

// jsonBody returns a js object or throws an error matching the {code: x, message: y} format, where x is a number and y is a string.
global.jsonBody = async res => {
  if (!res.ok) {
    // check if response is json before trying to parse it
    const text = await res.text()
    if (text.length > 1 && text[0] == '{') throw await JSON.parse(text)
    else throw { code: res.status, message: res.statusText }
  }

  return res.json()
}

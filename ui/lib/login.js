import Cookies from 'universal-cookie'

export function saveToVisitedOrgs(domain, orgName) {
  const cookies = new Cookies()

  let visitedOrgs = cookies.get('orgs') || []

  if (!visitedOrgs.find(x => x.url === domain)) {
    visitedOrgs.push({
      url: domain,
      name: orgName,
    })

    const baseDomain = currentBaseDomain()
    const cookieOptions = {
      path: '/',
    }

    // Only set domain attribute for valid FQDNs (not IPs, not localhost).
    // Cookie domains cannot contain ports (RFC 6265), and single-label hostnames
    // like "localhost" must not have a leading dot.
    const cleanHost = stripPort(window.location.host)
    if (!isIPAddress(cleanHost) && cleanHost.includes('.')) {
      cookieOptions.domain = `.${stripPort(baseDomain)}`
    }

    cookies.set('orgs', visitedOrgs, cookieOptions)
  }
}

export function currentBaseDomain() {
  let parts = window.location.host.split('.')
  if (parts.length > 2) {
    parts.shift() // remove the org
  }

  return parts.join('.') // return the domain without the org
}

// Strip port from a hostname (e.g. "localhost:9443" → "localhost")
// Cookie domains must not contain ports — RFC 6265 prohibits colons in domain attributes.
export function stripPort(host) {
  return host.split(':')[0]
}

// Helper function to check if a host is an IP address
export function isIPAddress(host) {
  // Remove port if present
  const hostWithoutPort = host.split(':')[0]

  // IPv4 pattern: validates each octet is 0-255
  const ipv4Pattern =
    /^((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.){3}(25[0-5]|(2[0-4]|1\d|[1-9]|)\d)$/

  // IPv6 pattern: comprehensive pattern supporting various formats
  // Matches full notation, compressed notation (::), and mixed IPv4/IPv6
  const ipv6Pattern =
    /^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:)|fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}|::(ffff(:0{1,4}){0,1}:){0,1}((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])|([0-9a-fA-F]{1,4}:){1,4}:((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9]))$/

  return ipv4Pattern.test(hostWithoutPort) || ipv6Pattern.test(hostWithoutPort)
}

export function formatPasswordRequirements(requirements) {
  return (
    'needs at least ' +
    requirements.reduce((value, currentValue, currentIndex) => {
      return (
        value +
        (currentIndex === requirements.length - 1
          ? requirements.length > 2
            ? ', and '
            : ' and '
          : ', ') +
        currentValue
      )
    })
  )
}

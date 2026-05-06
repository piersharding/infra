import { buildOIDCAuthURL } from '../../components/providers'

describe('OIDC provider login URL generation', () => {
  it('adds offline_access for non-google providers even when persisted scopes are stale', () => {
    const authURL = buildOIDCAuthURL(
      'https://idp.example.com/authorize',
      'client-id',
      ['openid', 'email'],
      'oidc',
      'https://app.example.com/signup/callback',
      'state-1'
    )

    expect(authURL.searchParams.get('scope')).toBe('openid email offline_access')
  })

  it('keeps google refresh-token parameters', () => {
    const authURL = buildOIDCAuthURL(
      'https://accounts.google.com/o/oauth2/v2/auth',
      'client-id',
      ['openid', 'email'],
      'google',
      'https://app.example.com/signup/callback',
      'state-2'
    )

    expect(authURL.searchParams.get('scope')).toBe('openid email offline_access')
    expect(authURL.searchParams.get('prompt')).toBe('consent')
    expect(authURL.searchParams.get('access_type')).toBe('offline')
  })
})

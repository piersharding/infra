# PKI Test Certificates

This directory contains TLS certificates used for testing the Infra server.

## Files

- `ca.crt` - Certificate Authority certificate (ECDSA P-256)
- `ca.key` - Certificate Authority private key
- `ca-public.pem` - CA public key in PEM format
- `localhost.crt` - Localhost server certificate signed by the CA
- `localhost.key` - Localhost server private key (ECDSA P-256)
- `generate-localhost-cert.sh` - Script to regenerate the localhost certificate

## Certificate Details

### CA Certificate
- **Type**: Certificate Authority (self-signed)
- **Algorithm**: ECDSA with P-256 curve
- **Subject**: CN=Infra Server
- **Validity**: 10 years
- **Purpose**: Sign server certificates for testing

### Localhost Certificate
- **Type**: Server certificate
- **Algorithm**: ECDSA with P-256 curve
- **Subject**: CN=localhost
- **SANs**: 
  - DNS: localhost
  - DNS: *.localhost
  - IP: 127.0.0.1
  - IP: ::1
- **Validity**: 10 years
- **Purpose**: TLS testing for local server instances

## Regenerating Certificates

If you need to regenerate the localhost certificate (e.g., if it expires or needs different SANs):

```bash
cd internal/server/testdata/pki
./generate-localhost-cert.sh
```

This script will:
1. Generate a new ECDSA P-256 private key for localhost
2. Create a certificate signing request (CSR)
3. Sign the certificate with the existing CA
4. Set appropriate file permissions
5. Clean up temporary files

## Verifying Certificates

To verify the localhost certificate is properly signed by the CA:

```bash
openssl verify -CAfile ca.crt localhost.crt
```

To view certificate details:

```bash
# View localhost certificate
openssl x509 -in localhost.crt -text -noout

# View CA certificate
openssl x509 -in ca.crt -text -noout
```

## Test Usage

These certificates are used by `tls_test.go` to test:
1. User-provided TLS certificates
2. Automatic certificate generation from CA
3. TLS configuration validation

The tests use the `golden.Get()` helper to load these files.

## Security Notes

⚠️ **These certificates are for testing only!**

- Do NOT use these certificates in production
- The private keys are committed to the repository
- These certificates have no security value outside of testing
- The CA is not trusted by any system by default

## Regenerating the CA (Advanced)

If you need to regenerate the CA itself (rarely needed):

```bash
# Generate CA private key (ECDSA P-256)
openssl ecparam -name prime256v1 -genkey -noout -out ca.key

# Create CA certificate (self-signed, 10 years)
openssl req -new -x509 -key ca.key -out ca.crt -days 3650 \
  -subj "/CN=Infra Server" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"

# Extract public key
openssl ec -in ca.key -pubout -out ca-public.pem

# Set permissions
chmod 600 ca.key
chmod 644 ca.crt ca-public.pem
```

After regenerating the CA, you must also regenerate the localhost certificate:

```bash
./generate-localhost-cert.sh
```

## Troubleshooting

### Test fails with "no such file or directory"

Make sure all certificate files exist:
```bash
ls -la internal/server/testdata/pki/
```

Expected files: `ca.crt`, `ca.key`, `localhost.crt`, `localhost.key`

If `localhost.crt` or `localhost.key` are missing, run:
```bash
./generate-localhost-cert.sh
```

### Test fails with "certificate has expired"

Regenerate the localhost certificate:
```bash
./generate-localhost-cert.sh
```

### Test fails with "certificate signed by unknown authority"

The localhost certificate may not be properly signed by the CA. Verify:
```bash
openssl verify -CAfile ca.crt localhost.crt
```

If verification fails, regenerate the localhost certificate.
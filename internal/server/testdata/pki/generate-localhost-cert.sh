#!/bin/bash

# Script to generate localhost TLS certificates for testing
# This creates a localhost certificate signed by the existing CA

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Generating localhost certificate and private key..."

# Check if CA files exist
if [ ! -f "ca.crt" ] || [ ! -f "ca.key" ]; then
    echo "Error: CA certificate (ca.crt) or CA key (ca.key) not found"
    echo "Please ensure the CA files exist in $SCRIPT_DIR"
    exit 1
fi

# Generate private key for localhost
openssl ecparam -name prime256v1 -genkey -noout -out localhost.key

# Create OpenSSL configuration for certificate signing
cat > localhost.cnf <<EOF
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req
prompt = no

[req_distinguished_name]
CN = localhost

[v3_req]
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = *.localhost
IP.1 = 127.0.0.1
IP.2 = ::1
EOF

# Generate certificate signing request
openssl req -new -key localhost.key -out localhost.csr -config localhost.cnf

# Sign the certificate with the CA
openssl x509 -req \
    -in localhost.csr \
    -CA ca.crt \
    -CAkey ca.key \
    -CAcreateserial \
    -out localhost.crt \
    -days 3650 \
    -sha256 \
    -extensions v3_req \
    -extfile localhost.cnf

# Clean up temporary files
rm -f localhost.csr localhost.cnf ca.srl

# Set appropriate permissions
chmod 644 localhost.crt
chmod 600 localhost.key

echo "✓ Successfully generated:"
echo "  - localhost.crt (certificate)"
echo "  - localhost.key (private key)"
echo ""
echo "Certificate details:"
openssl x509 -in localhost.crt -text -noout | grep -A2 "Subject Alternative Name" || true
openssl x509 -in localhost.crt -noout -dates

echo ""
echo "Done!"

package encrypt

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestSealAndUnseal(t *testing.T) {
	tmp := t.TempDir()
	rootKeyPath := filepath.Join(tmp, "root-key")
	assert.NilError(t, CreateRootKey(rootKeyPath))

	key, err := CreateDataKey(rootKeyPath)
	assert.NilError(t, err)

	secretMessage := "This is the message"
	encrypted, err := Seal(key, []byte(secretMessage))
	assert.NilError(t, err)

	unsealed, err := Unseal(key, encrypted)
	assert.NilError(t, err)
	assert.Equal(t, secretMessage, string(unsealed))
}

func TestDecryptDataKey(t *testing.T) {
	tmp := t.TempDir()
	rootKeyPath := filepath.Join(tmp, "root-key")
	assert.NilError(t, CreateRootKey(rootKeyPath))

	dataKey, err := CreateDataKey(rootKeyPath)
	assert.NilError(t, err)

	actual, err := DecryptDataKey(rootKeyPath, dataKey.Encrypted)
	assert.NilError(t, err)

	assert.DeepEqual(t, actual.unencrypted, dataKey.unencrypted)
}

func TestCheckedPayloadLength32(t *testing.T) {
	length, err := checkedPayloadLength32("ciphertext", 16)
	assert.NilError(t, err)
	assert.Equal(t, length, uint32(16))
}

func TestCheckedPayloadLength8(t *testing.T) {
	t.Run("within limits", func(t *testing.T) {
		length, err := checkedPayloadLength8("algorithm", 16)
		assert.NilError(t, err)
		assert.Equal(t, length, uint8(16))
	})

	t.Run("exceeds limits", func(t *testing.T) {
		_, err := checkedPayloadLength8("algorithm", 256)
		assert.ErrorContains(t, err, "algorithm length 256 exceeds max 255")
	})
}

func TestMarshalPayloadRejectsLongFields(t *testing.T) {
	t.Run("algorithm", func(t *testing.T) {
		_, err := marshalPayload(&encryptedPayload{
			Ciphertext: []byte("ciphertext"),
			Algorithm:  strings.Repeat("a", math.MaxUint8+1),
			KeyID:      []byte("key"),
			RootKeyID:  "root",
			Nonce:      []byte("nonce"),
		})

		assert.ErrorContains(t, err, "algorithm length 256 exceeds max 255")
	})

	t.Run("key id", func(t *testing.T) {
		_, err := marshalPayload(&encryptedPayload{
			Ciphertext: []byte("ciphertext"),
			Algorithm:  "aesgcm",
			KeyID:      []byte(strings.Repeat("k", math.MaxUint8+1)),
			RootKeyID:  "root",
			Nonce:      []byte("nonce"),
		})

		assert.ErrorContains(t, err, "key id length 256 exceeds max 255")
	})

	t.Run("root key id", func(t *testing.T) {
		_, err := marshalPayload(&encryptedPayload{
			Ciphertext: []byte("ciphertext"),
			Algorithm:  "aesgcm",
			KeyID:      []byte("key"),
			RootKeyID:  strings.Repeat("r", math.MaxUint8+1),
			Nonce:      []byte("nonce"),
		})

		assert.ErrorContains(t, err, "root key id length 256 exceeds max 255")
	})

	t.Run("nonce", func(t *testing.T) {
		_, err := marshalPayload(&encryptedPayload{
			Ciphertext: []byte("ciphertext"),
			Algorithm:  "aesgcm",
			KeyID:      []byte("key"),
			RootKeyID:  "root",
			Nonce:      []byte(strings.Repeat("n", math.MaxUint8+1)),
		})

		assert.ErrorContains(t, err, "nonce length 256 exceeds max 255")
	})
}

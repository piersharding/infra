package encrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type SymmetricKey struct {
	// unencrypted is the unencrypted data. This field *MUST NOT* be persisted.
	unencrypted []byte
	// Encrypted is the encrypted data.
	Encrypted []byte `json:"key"`
	// Algorithm is the algorithm used for encryption.
	Algorithm string `json:"alg"`
	// RootKeyId is the ID of the root key used to encrypt the data key.
	RootKeyID string `json:"rkid"`
}

// cryptoRandRead is a safe read from crypto/rand, checking errors and number of bytes read, erroring if we don't get enough
func cryptoRandRead(length int) ([]byte, error) {
	b := make([]byte, length)

	i, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("crypto/rand read: %w", err)
	}

	if i != length {
		return nil, fmt.Errorf("could not read %d random characters from crypto/rand, only got %d", length, i)
	}

	return b, nil
}

// Seal encrypts plaintext with a decrypted data key and returns it in base64
func Seal(key *SymmetricKey, plain []byte) ([]byte, error) {
	if len(key.unencrypted) != 32 {
		return nil, fmt.Errorf("key is the wrong size %v, expected 32 bytes", len(key.unencrypted))
	}

	blk, err := aes.NewCipher(key.unencrypted)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}

	nonce, err := cryptoRandRead(aesgcm.NonceSize())
	if err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	encrypted := aesgcm.Seal(nil, nonce, plain, nil)

	keyID, err := checksum(key.Encrypted)
	if err != nil {
		return nil, err
	}

	payload := encryptedPayload{
		KeyID:      keyID,
		Ciphertext: encrypted,
		Algorithm:  key.Algorithm,
		RootKeyID:  key.RootKeyID,
		Nonce:      nonce,
	}

	marshalled, err := marshalPayload(&payload)
	if err != nil {
		return nil, err
	}

	encoded := make([]byte, base64.RawStdEncoding.EncodedLen(len(marshalled)))
	base64.RawStdEncoding.Encode(encoded, marshalled)

	return encoded, nil
}

// Unseal decrypts base64-encoded ciphertext with a decrypted data key
func Unseal(key *SymmetricKey, encoded []byte) ([]byte, error) {
	if len(key.unencrypted) == 0 {
		return nil, errors.New("missing key")
	}

	encrypted := make([]byte, base64.RawStdEncoding.DecodedLen(len(encoded)))
	_, err := base64.RawStdEncoding.Decode(encrypted, encoded)
	if err != nil {
		return nil, fmt.Errorf("decoding payload: %w", err)
	}

	payload := &encryptedPayload{}
	if err := unmarshalPayload(encrypted, payload); err != nil {
		return nil, fmt.Errorf("unmarshalling: %w", err)
	}

	ck, err := checksum(key.Encrypted)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(ck, payload.KeyID) {
		return nil, fmt.Errorf("supplied key cannot decrypt this message; wrong key was used")
	}

	blk, err := aes.NewCipher(key.unencrypted)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	plaintext, err := aesgcm.Open(nil, payload.Nonce, payload.Ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("opening seal: %w", err)
	}

	return plaintext, nil
}

func checksum(b []byte) ([]byte, error) {
	keyIDChecksum := sha256.New()

	ln, err := keyIDChecksum.Write(b)
	if err != nil {
		return nil, fmt.Errorf("creating checksum: %w", err)
	}

	if ln != len(b) {
		return nil, fmt.Errorf("checksum write len")
	}

	return keyIDChecksum.Sum(nil)[:4], nil
}

type encryptedPayload struct {
	Ciphertext []byte // base64 encoded
	Algorithm  string // name of the algorithm used to encrypt the Ciphertext
	KeyID      []byte // A unique identifier for the key used. Could be checksum or a version number
	RootKeyID  string // id of the root key the Key field is encrypted with
	Nonce      []byte // must be crypto-random unique every time, size = block size
}

func checkedPayloadLength32(field string, length int) (uint32, error) {
	if length < 0 || int64(length) > math.MaxUint32 {
		return 0, fmt.Errorf("%s length %d exceeds max %d", field, length, uint64(math.MaxUint32))
	}

	//nolint:gosec // length is range-checked against MaxUint32 immediately above.
	return uint32(length), nil
}

func checkedPayloadLength8(field string, length int) (uint8, error) {
	if length < 0 || int64(length) > math.MaxUint8 {
		return 0, fmt.Errorf("%s length %d exceeds max %d", field, length, uint64(math.MaxUint8))
	}

	//nolint:gosec // length is range-checked against MaxUint8 immediately above.
	return uint8(length), nil
}

func unmarshalPayload(mp []byte, p *encryptedPayload) error {
	b := bytes.NewBuffer(mp)

	var ln uint32
	if err := binary.Read(b, binary.BigEndian, &ln); err != nil {
		return err
	}

	p.Ciphertext = make([]byte, ln)
	if err := binary.Read(b, binary.BigEndian, &p.Ciphertext); err != nil {
		return err
	}

	var length uint8
	if err := binary.Read(b, binary.BigEndian, &length); err != nil {
		return err
	}

	alg := make([]byte, length)
	if err := binary.Read(b, binary.BigEndian, &alg); err != nil {
		return err
	}

	p.Algorithm = string(alg)

	if err := binary.Read(b, binary.BigEndian, &length); err != nil {
		return err
	}

	p.KeyID = make([]byte, length)
	if err := binary.Read(b, binary.BigEndian, &p.KeyID); err != nil {
		return err
	}

	if err := binary.Read(b, binary.BigEndian, &length); err != nil {
		return err
	}

	rootKeyID := make([]byte, length)
	if err := binary.Read(b, binary.BigEndian, &rootKeyID); err != nil {
		return err
	}

	p.RootKeyID = string(rootKeyID)

	if err := binary.Read(b, binary.BigEndian, &length); err != nil {
		return err
	}

	p.Nonce = make([]byte, length)
	if err := binary.Read(b, binary.BigEndian, &p.Nonce); err != nil {
		return err
	}

	return nil
}

func marshalPayload(p *encryptedPayload) ([]byte, error) {
	b := bytes.NewBuffer(nil)

	ciphertextLength, err := checkedPayloadLength32("ciphertext", len(p.Ciphertext))
	if err != nil {
		return nil, err
	}
	if err := binary.Write(b, binary.BigEndian, ciphertextLength); err != nil {
		return nil, err
	}

	if err := binary.Write(b, binary.BigEndian, p.Ciphertext); err != nil {
		return nil, err
	}

	algorithmLength, err := checkedPayloadLength8("algorithm", len(p.Algorithm))
	if err != nil {
		return nil, err
	}
	if err := binary.Write(b, binary.BigEndian, algorithmLength); err != nil {
		return nil, err
	}

	if err := binary.Write(b, binary.BigEndian, []byte(p.Algorithm)); err != nil {
		return nil, err
	}

	keyIDLength, err := checkedPayloadLength8("key id", len(p.KeyID))
	if err != nil {
		return nil, err
	}
	if err := binary.Write(b, binary.BigEndian, keyIDLength); err != nil {
		return nil, err
	}

	if err := binary.Write(b, binary.BigEndian, p.KeyID); err != nil {
		return nil, err
	}

	rootKeyIDLength, err := checkedPayloadLength8("root key id", len(p.RootKeyID))
	if err != nil {
		return nil, err
	}
	if err := binary.Write(b, binary.BigEndian, rootKeyIDLength); err != nil {
		return nil, err
	}

	if err := binary.Write(b, binary.BigEndian, []byte(p.RootKeyID)); err != nil {
		return nil, err
	}

	nonceLength, err := checkedPayloadLength8("nonce", len(p.Nonce))
	if err != nil {
		return nil, err
	}
	if err := binary.Write(b, binary.BigEndian, nonceLength); err != nil {
		return nil, err
	}

	if err := binary.Write(b, binary.BigEndian, p.Nonce); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

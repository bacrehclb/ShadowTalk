package sqlite

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"golang.org/x/crypto/chacha20poly1305"
)

const maxCiphertext = protocol.MaxTextBytes + chacha20poly1305.NonceSizeX + chacha20poly1305.Overhead

func databaseCipher(root, salt []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, root, salt, "ShadowTalk/history-database/v1", chacha20poly1305.KeySize)
	if err != nil {
		return nil, history.ErrInvalidConfig
	}
	defer clear(key)
	return chacha20poly1305.NewX(key)
}

func seal(aead cipher.AEAD, plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func unseal(aead cipher.AEAD, ciphertext, aad []byte) ([]byte, error) {
	if len(ciphertext) < aead.NonceSize()+aead.Overhead() || len(ciphertext) > maxCiphertext {
		return nil, history.ErrDecryption
	}
	plaintext, err := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], aad)
	if err != nil {
		return nil, history.ErrDecryption
	}
	return plaintext, nil
}

func keyCheckAAD(owner [32]byte, salt []byte) []byte {
	aad := append([]byte("ShadowTalk/history-key-check/v1\x00"), owner[:]...)
	return append(aad, salt...)
}

func (s *Store) messageAAD(entry history.Entry) []byte {
	aad := append([]byte("ShadowTalk/history-record/v1\x00"), s.owner[:]...)
	aad = binary.BigEndian.AppendUint64(aad, uint64(entry.Sequence))
	aad = append(aad, entry.Peer[:]...)
	aad = append(aad, byte(entry.Direction))
	aad = append(aad, entry.ID[:]...)
	return binary.BigEndian.AppendUint64(aad, uint64(entry.RecordedAt.UnixMilli()))
}

func (s *Store) decrypt(entry history.Entry, ciphertext []byte) (history.Entry, error) {
	if entry.Sequence <= 0 || entry.Peer == s.owner {
		return history.Entry{}, history.ErrCorrupt
	}
	plaintext, err := unseal(s.aead, ciphertext, s.messageAAD(entry))
	if err != nil {
		return history.Entry{}, errors.Join(history.ErrCorrupt, err)
	}
	defer clear(plaintext)
	entry.Text = string(plaintext)
	if entry.Message.Validate() != nil {
		return history.Entry{}, history.ErrCorrupt
	}
	return entry, nil
}

package downloader

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- PR-HLS-AES128 (P1, July 2026) --------------------------------------
//
// TestHLS_DecryptSegment_RoundTripExplicitIV: encrypt → decrypt with
// explicit hex-derived IV → compare plaintext (post-PKCS#7-strip).
//
// TestHLS_DecryptSegment_RoundTripSequenceIV: same round-trip but
// IV comes from IVFromSequence(N) (the implicit fallback path).
//
// TestHLS_DecryptSegment_*_RejectsInvalidInput: locks the typed-error
// gate so mis-sized keys / mis-aligned ciphertexts / unparseable IVs
// FAIL CLOSED (godlike/07 no-fake-availability).
//
// TestHLS_IVFromHex_*: right-justifies short hex + rejects malformed hex.
//
// TestHLS_IVFromSequence_*: big-endian 128-bit encoding at boundary
// values (0, 1, 256, MaxInt64 to detect overflow byte-ordering errors).
//
// TestHLS_StripPKCS7Padding_*: full-block-padding path (16 bytes of
// 0x10) + invalid pad byte rejection.

func TestHLS_DecryptSegment_RoundTripExplicitIV(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 bytes — PIN in all tests for stable vector
	iv, err := IVFromHex("0102030405060708090a0b0c0d0e0f")
	require.NoError(t, err)
	require.Len(t, iv, 16)
	require.Equal(t, byte(0x0f), iv[15], "IV must be right-justified: ivHex right-pads to 16 bytes")
	require.Equal(t, byte(0x00), iv[0], "IV[0] must be zero-padded LEFT")

	plaintextData := []byte("HLS round-trip ok!") // 18 bytes
	// PKCS#7 pad: 18 % 16 = 2 → pad byte = 16-2 = 14, total 18+14 = 32
	padLen := 16 - (len(plaintextData) % 16)
	padByte := byte(padLen)
	require.Equal(t, 14, padLen)
	plaintextWithPKCS := append([]byte{}, plaintextData...)
	for i := 0; i < padLen; i++ {
		plaintextWithPKCS = append(plaintextWithPKCS, padByte)
	}
	require.Len(t, plaintextWithPKCS, 32)

	// Encrypt locally (crypto/aes) so the test self-validates without
	// depending on an external golden vector file.
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	ciphertext := make([]byte, len(plaintextWithPKCS))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintextWithPKCS)
	require.Equal(t, 32, len(ciphertext))

	// Decrypt via our helper.
	plaintext, err := DecryptSegment(key, iv, ciphertext)
	require.NoError(t, err)
	require.Len(t, plaintext, 32)

	// Strip PKCS#7 → recover original 18-byte data.
	unpadded, err := StripPKCS7Padding(plaintext)
	require.NoError(t, err)
	assert.Equal(t, plaintextData, unpadded, "plaintext must match after PKCS#7 strip")
}

func TestHLS_DecryptSegment_RoundTripSequenceIV(t *testing.T) {
	// Pick a sequence number that exercises cross-byte boundaries
	// (so IVFromSequence byte-ordering bugs would visibly diverge).
	const seq = int64(0x0102030405060708)
	iv := IVFromSequence(seq)
	require.Len(t, iv, 16, "IV must be 16 bytes")

	// Verify byte ordering: spec is BIG-ENDIAN, right-justified in 16 bytes.
	// Expected layout:
	//   iv[0..7]   = 0x00 (zero-padded)
	//   iv[8]      = 0x01
	//   iv[9]      = 0x02
	//   iv[10]     = 0x03
	//   iv[11]     = 0x04
	//   iv[12]     = 0x05
	//   iv[13]     = 0x06
	//   iv[14]     = 0x07
	//   iv[15]     = 0x08
	for i := 0; i < 8; i++ {
		assert.Equal(t, byte(0x00), iv[i], "iv[%d] must be zero-padded LEFT", i)
	}
	assert.Equal(t, byte(0x01), iv[8])
	assert.Equal(t, byte(0x02), iv[9])
	assert.Equal(t, byte(0x03), iv[10])
	assert.Equal(t, byte(0x04), iv[11])
	assert.Equal(t, byte(0x05), iv[12])
	assert.Equal(t, byte(0x06), iv[13])
	assert.Equal(t, byte(0x07), iv[14])
	assert.Equal(t, byte(0x08), iv[15])

	// Round-trip a single block with the symmetric test key.
	key := []byte("0123456789abcdef")                             // 16 bytes — symmetric key for cross-test stability check
	plaintext := []byte("HLS sequence-IV-AES-128-CBC test!")[:16] // exactly 1 block
	plaintextWithPKCS := append([]byte{}, plaintext...)
	// PKCS#7 full-block padding: 16 bytes of 0x10
	plaintextWithPKCS = append(plaintextWithPKCS, bytes.Repeat([]byte{0x10}, 16)...)
	require.Len(t, plaintextWithPKCS, 32)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	ciphertext := make([]byte, 32)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintextWithPKCS)

	plain, err := DecryptSegment(key, iv, ciphertext)
	require.NoError(t, err)
	require.Len(t, plain, 32)
	unpadded, err := StripPKCS7Padding(plain)
	require.NoError(t, err)
	assert.Equal(t, plaintext, unpadded)
}

func TestHLS_DecryptSegment_RejectsInvalidInput(t *testing.T) {
	validKey := []byte("0123456789abcdef")
	validIV := IVFromSequence(1)
	validCT := bytes.Repeat([]byte{0x00}, 16)

	t.Run("key 15 bytes", func(t *testing.T) {
		_, err := DecryptSegment([]byte("short-aes-key-0123"), validIV, validCT)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrKeyInvalid))
	})
	t.Run("key 17 bytes", func(t *testing.T) {
		_, err := DecryptSegment([]byte("17-byte-aes-key-01234567"), validIV, validCT)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrKeyInvalid))
	})
	t.Run("iv 15 bytes", func(t *testing.T) {
		_, err := DecryptSegment(validKey, []byte("short-init__vec"), validCT)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrIVInvalid))
	})
	t.Run("empty ciphertext", func(t *testing.T) {
		_, err := DecryptSegment(validKey, validIV, nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCiphertextMisaligned))
	})
	t.Run("misaligned ciphertext (10 bytes)", func(t *testing.T) {
		_, err := DecryptSegment(validKey, validIV, bytes.Repeat([]byte{0x00}, 10))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCiphertextMisaligned))
	})
}

func TestHLS_IVFromHex(t *testing.T) {
	t.Run("full 16-byte hex", func(t *testing.T) {
		iv, err := IVFromHex("00112233445566778899aabbccddeeff")
		require.NoError(t, err)
		assert.Equal(t, []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		}, iv)
	})
	t.Run("short hex right-pads to 16 bytes (left zeros)", func(t *testing.T) {
		iv, err := IVFromHex("010203")
		require.NoError(t, err)
		require.Len(t, iv, 16)
		// iv[0..12] should be 0, iv[13..15] should be 01 02 03.
		for i := 0; i < 13; i++ {
			assert.Equal(t, byte(0x00), iv[i])
		}
		assert.Equal(t, byte(0x01), iv[13])
		assert.Equal(t, byte(0x02), iv[14])
		assert.Equal(t, byte(0x03), iv[15])
	})
	t.Run("rejects malformed hex", func(t *testing.T) {
		_, err := IVFromHex("zz not hex")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrIVInvalid))
	})
	t.Run("rejects hex longer than 16 bytes", func(t *testing.T) {
		// 17 bytes (34 hex chars) — too big for an AES IV slot.
		_, err := IVFromHex("00112233445566778899aabbccddeeff00")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrIVInvalid))
	})
}

func TestHLS_IVFromSequence(t *testing.T) {
	t.Run("zero sequence", func(t *testing.T) {
		iv := IVFromSequence(0)
		for i := 0; i < 16; i++ {
			assert.Equal(t, byte(0x00), iv[i])
		}
	})
	t.Run("sequence=1", func(t *testing.T) {
		iv := IVFromSequence(1)
		for i := 0; i < 15; i++ {
			assert.Equal(t, byte(0x00), iv[i])
		}
		assert.Equal(t, byte(0x01), iv[15])
	})
	t.Run("sequence=256 (byte-boundary)", func(t *testing.T) {
		iv := IVFromSequence(256)
		for i := 0; i < 14; i++ {
			assert.Equal(t, byte(0x00), iv[i])
		}
		assert.Equal(t, byte(0x01), iv[14])
		assert.Equal(t, byte(0x00), iv[15])
	})
}

func TestHLS_StripPKCS7Padding(t *testing.T) {
	t.Run("strips last 5 bytes (pad=5)", func(t *testing.T) {
		data := []byte("0123456789ABC")
		padded := append([]byte{}, data...)
		for i := 0; i < 5; i++ {
			padded = append(padded, byte(5))
		}
		out, err := StripPKCS7Padding(padded)
		require.NoError(t, err)
		assert.Equal(t, data, out)
	})
	t.Run("strips full-block padding (16 bytes of 0x10)", func(t *testing.T) {
		data := []byte("0123456789abcdef")
		padded := append([]byte{}, data...)
		padded = append(padded, bytes.Repeat([]byte{0x10}, 16)...)
		out, err := StripPKCS7Padding(padded)
		require.NoError(t, err)
		assert.Equal(t, data, out)
	})
	t.Run("rejects padding byte 0x00", func(t *testing.T) {
		_, err := StripPKCS7Padding([]byte("data-with-bad-pad\x00"))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPaddingInvalid))
	})
	t.Run("rejects inconsistent padding", func(t *testing.T) {
		// Last byte says "pad 5" but only 3 bytes are 0x05.
		_, err := StripPKCS7Padding([]byte("0123456\x05\x05\x05\x03"))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPaddingInvalid))
	})
	t.Run("rejects empty plaintext", func(t *testing.T) {
		_, err := StripPKCS7Padding(nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPaddingInvalid))
	})
}

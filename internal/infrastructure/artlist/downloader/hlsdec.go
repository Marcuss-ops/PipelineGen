// Package downloader — hlsdec.go: HLS AES-128-CBC segment decryption
// (PR-HLS-AES128, P1, July 2026).
//
// Pure-function helper. No I/O, no port wiring, no transport gating.
// This file owns the AES-128-CBC + IV-resolution math per HLS spec
// §4.3.2.4 ("The IV attribute, if present, MUST be interpreted as
// hexadecimal") + the implicit-sequence-number fallback (RFC 8216).
//
// godlike/06 SSOT: this is the SINGLE canonical owner of "how to
// decrypt an AES-128 HLS segment" in Go. Any hlsdec-like helper
// elsewhere in the codebase is a bug or a deprecation candidate.
//
// KNOWN LIMITATION (PR-HLS-AES128 P1 scope): the live Artlist HLS
// pipeline currently decrypts in Node
// (node-scraper/src/artlist/download.js::downloadHLSWithCookies via
// the browser session). Go-side decryption is wired via this helper
// for tests + future Go-side fetching. Resolver.reorder + ffprobe
// validation in this PR are the operator-visible reliability win —
// the AES-128 math is here for the next step (Go-side fetcher or
// Node-callable-Go-binary pipe).
package downloader

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrKeyInvalid is the typed sentinel DecryptSegment returns when the
// supplied AES-128 key is not exactly 16 bytes. Used by callers that
// want to branch on intent (godlike/07 no-fake-availability: a
// mis-sized key must NEVER silently produce a wrong plaintext).
var ErrKeyInvalid = errors.New("hlsdec: AES-128 key must be exactly 16 bytes")

// ErrIVInvalid is the typed sentinel for IV resolution failures
// (wrong size, malformed hex, sequence overflow fallback path).
var ErrIVInvalid = errors.New("hlsdec: IV resolution failed")

// ErrCiphertextMisaligned is the typed sentinel for ciphertext
// length not being a multiple of aes.BlockSize. CBC requires whole
// blocks; partial blocks are a sign of upstream truncation, not a
// valid ciphertext we should attempt to decrypt.
var ErrCiphertextMisaligned = errors.New("hlsdec: ciphertext length is not a multiple of 16 bytes")

// ErrPaddingInvalid is the typed sentinel for PKCS#7 padding violations.
var ErrPaddingInvalid = errors.New("hlsdec: PKCS#7 padding invalid")

// DecryptSegment decrypts one HLS segment with AES-128-CBC.
//
// Per HLS spec §4.3.2.4:
//   - The key MUST be exactly 16 bytes (AES-128).
//   - The IV MUST be exactly 16 bytes. Use IVFromHex for the
//     explicit `IV=0x...` attribute, or IVFromSequence for the
//     implicit per-segment sequence-number fallback.
//   - Input ciphertext MUST be a multiple of 16 bytes (CBC block
//     boundary). Truncated upstream streams will return
//     ErrCiphertextMisaligned rather than silently producing wrong
//     bytes (godlike/07 no-fake-availability).
//
// Output preserves PKCS#7 padding — callers that need the unpadded
// plaintext MUST run StripPKCS7Padding. Keeping the padding by
// default lets a caller byte-compare against an upstream producer
// that doesn't strip (some HLS-aligned muxer chains).
func DecryptSegment(key, iv, ciphertext []byte) ([]byte, error) {
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("%w (got %d bytes)", ErrKeyInvalid, len(key))
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("%w (got %d bytes)", ErrIVInvalid, len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w (got %d bytes; need non-zero multiple of %d)",
			ErrCiphertextMisaligned, len(ciphertext), aes.BlockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("hlsdec: aes.NewCipher: %w", err)
	}
	decrypter := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	decrypter.CryptBlocks(plaintext, ciphertext)
	return plaintext, nil
}

// IVFromHex parses the explicit IV from the EXT-X-KEY attribute.
//
// The HLS spec encodes the IV as `IV=0x<hex>` WITHOUT a leading 0x
// in the actual attribute and WITHOUT PKCS-style padding — short hex
// strings are right-justified to 16 bytes (left-padded with zeros),
// matching the spec's "If the attribute is not present, the
// implementation MUST use the media sequence number as the IV"
// inverse: small explicit IVs are zero-padded on the LEFT.
func IVFromHex(ivHex string) ([]byte, error) {
	raw, err := hex.DecodeString(ivHex)
	if err != nil {
		return nil, fmt.Errorf("%w: parse IV=0x%s: %v", ErrIVInvalid, ivHex, err)
	}
	if len(raw) > aes.BlockSize {
		return nil, fmt.Errorf("%w: IV=0x%s exceeds %d bytes (got %d)",
			ErrIVInvalid, ivHex, aes.BlockSize, len(raw))
	}
	iv := make([]byte, aes.BlockSize)
	copy(iv[aes.BlockSize-len(raw):], raw) // right-justify hex; LEFT zero-pad
	return iv, nil
}

// IVFromSequence computes the implicit IV from the segment's media
// sequence number per HLS spec §4.3.2.4 + RFC 8216 §5.2: the sequence
// number is encoded as a 128-bit BIG-ENDIAN integer, RIGHT-justified
// in the 16-byte IV (left-padded with zeros). This is the
// default-fallback for playlists that omit the IV= attribute.
func IVFromSequence(seq int64) []byte {
	if seq < 0 {
		// HLS sequence numbers are non-negative. Negative inputs would
		// silently produce a wrong IV; fail closed at the call site
		// (decode to uint64) but don't panic in the helper.
		seq = 0
	}
	iv := make([]byte, aes.BlockSize)
	// Write the lower 8 bytes BIG-ENDIAN into the LAST 8 slots.
	for i := 7; i >= 0; i-- {
		iv[15-i] = byte(seq >> (i * 8))
	}
	return iv
}

// StripPKCS7Padding removes PKCS#7 padding from a decrypted segment
// (RFC 5246 §6.3). Validates every padding byte matches the count
// before returning the unpadded plaintext. Returns ErrPaddingInvalid
// on any inconsistency (caller-side state corruption gate).
//
// Spec note: PKCS#7 mandates padding even when the plaintext is
// exactly a multiple of the block size — full-block padding of value
// 0x10 is valid and MUST be stripped (will check 16 == 16).
func StripPKCS7Padding(plaintext []byte) ([]byte, error) {
	n := len(plaintext)
	if n == 0 {
		return nil, fmt.Errorf("%w: empty plaintext", ErrPaddingInvalid)
	}
	pad := int(plaintext[n-1])
	if pad == 0 || pad > aes.BlockSize || pad > n {
		return nil, fmt.Errorf("%w: pad byte 0x%02x out of range (size=%d, blocksize=%d)",
			ErrPaddingInvalid, plaintext[n-1], n, aes.BlockSize)
	}
	for i := n - pad; i < n; i++ {
		if plaintext[i] != byte(pad) {
			return nil, fmt.Errorf("%w: pad byte 0x%02x at index %d != expected 0x%02x",
				ErrPaddingInvalid, plaintext[i], i, pad)
		}
	}
	return plaintext[:n-pad], nil
}

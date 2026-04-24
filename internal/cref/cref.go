//go:build cgo

// Package cref wraps the C reference brotli encoder/decoder for use in tests.
package cref

/*
#cgo CFLAGS: -I${SRCDIR}/../../brotli-ref/c/include
#cgo LDFLAGS: ${SRCDIR}/../../lib/libbrotli_cref.a -lm

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <brotli/decode.h>
#include <brotli/encode.h>

// crefCompress compresses src using the streaming API with a single
// BROTLI_OPERATION_FINISH call.
static int crefCompress(
    const uint8_t *src, size_t src_len,
    int quality, int lgwin, uint32_t size_hint,
    uint8_t **out, size_t *out_len) {

    BrotliEncoderState *s = BrotliEncoderCreateInstance(NULL, NULL, NULL);
    if (!s) return 0;

    BrotliEncoderSetParameter(s, BROTLI_PARAM_QUALITY, (uint32_t)quality);
    BrotliEncoderSetParameter(s, BROTLI_PARAM_LGWIN, (uint32_t)lgwin);
    BrotliEncoderSetParameter(s, BROTLI_PARAM_MODE, BROTLI_MODE_GENERIC);
    if (size_hint > 0)
        BrotliEncoderSetParameter(s, BROTLI_PARAM_SIZE_HINT, size_hint);

    size_t cap = BrotliEncoderMaxCompressedSize(src_len);
    if (cap == 0) cap = src_len + 1024;
    uint8_t *buf = malloc(cap);
    if (!buf) { BrotliEncoderDestroyInstance(s); return 0; }

    size_t total = 0;
    const uint8_t *next_in = src;
    size_t avail_in = src_len;

    for (;;) {
        uint8_t *next_out = buf + total;
        size_t avail_out = cap - total;
        if (!BrotliEncoderCompressStream(s, BROTLI_OPERATION_FINISH,
                &avail_in, &next_in, &avail_out, &next_out, NULL)) {
            free(buf);
            BrotliEncoderDestroyInstance(s);
            return 0;
        }
        total = (size_t)(next_out - buf);
        if (BrotliEncoderIsFinished(s)) break;
        if (total == cap) {
            cap *= 2;
            buf = realloc(buf, cap);
            if (!buf) { BrotliEncoderDestroyInstance(s); return 0; }
        }
    }

    BrotliEncoderDestroyInstance(s);
    *out = buf;
    *out_len = total;
    return 1;
}

// crefCompressDict compresses src with a raw prefix compound dictionary.
static int crefCompressDict(
    const uint8_t *src, size_t src_len,
    const uint8_t *dict, size_t dict_len,
    int quality, int lgwin, uint32_t size_hint,
    uint8_t **out, size_t *out_len) {

    BrotliEncoderState *s = BrotliEncoderCreateInstance(NULL, NULL, NULL);
    if (!s) return 0;

    BrotliEncoderSetParameter(s, BROTLI_PARAM_QUALITY, (uint32_t)quality);
    BrotliEncoderSetParameter(s, BROTLI_PARAM_LGWIN, (uint32_t)lgwin);
    BrotliEncoderSetParameter(s, BROTLI_PARAM_MODE, BROTLI_MODE_GENERIC);
    if (size_hint > 0)
        BrotliEncoderSetParameter(s, BROTLI_PARAM_SIZE_HINT, size_hint);

    BrotliEncoderPreparedDictionary *pd =
        BrotliEncoderPrepareDictionary(BROTLI_SHARED_DICTIONARY_RAW,
            dict_len, dict, quality, NULL, NULL, NULL);
    if (!pd) { BrotliEncoderDestroyInstance(s); return 0; }
    if (!BrotliEncoderAttachPreparedDictionary(s, pd)) {
        BrotliEncoderDestroyPreparedDictionary(pd);
        BrotliEncoderDestroyInstance(s);
        return 0;
    }

    size_t cap = BrotliEncoderMaxCompressedSize(src_len);
    if (cap == 0) cap = src_len + 1024;
    uint8_t *buf = malloc(cap);
    if (!buf) {
        BrotliEncoderDestroyPreparedDictionary(pd);
        BrotliEncoderDestroyInstance(s);
        return 0;
    }

    size_t total = 0;
    const uint8_t *next_in = src;
    size_t avail_in = src_len;

    for (;;) {
        uint8_t *next_out = buf + total;
        size_t avail_out = cap - total;
        if (!BrotliEncoderCompressStream(s, BROTLI_OPERATION_FINISH,
                &avail_in, &next_in, &avail_out, &next_out, NULL)) {
            free(buf);
            BrotliEncoderDestroyPreparedDictionary(pd);
            BrotliEncoderDestroyInstance(s);
            return 0;
        }
        total = (size_t)(next_out - buf);
        if (BrotliEncoderIsFinished(s)) break;
        if (total == cap) {
            cap *= 2;
            buf = realloc(buf, cap);
            if (!buf) {
                BrotliEncoderDestroyPreparedDictionary(pd);
                BrotliEncoderDestroyInstance(s);
                return 0;
            }
        }
    }

    BrotliEncoderDestroyPreparedDictionary(pd);
    BrotliEncoderDestroyInstance(s);
    *out = buf;
    *out_len = total;
    return 1;
}

// crefDecompress decompresses brotli-encoded data.
static int crefDecompress(
    const uint8_t *src, size_t src_len,
    uint8_t **out, size_t *out_len) {

    size_t cap = src_len * 4;
    if (cap < 1024) cap = 1024;
    uint8_t *buf = malloc(cap);
    if (!buf) return 0;

    BrotliDecoderState *s = BrotliDecoderCreateInstance(NULL, NULL, NULL);
    if (!s) { free(buf); return 0; }

    size_t total = 0;
    const uint8_t *next_in = src;
    size_t avail_in = src_len;

    for (;;) {
        uint8_t *next_out = buf + total;
        size_t avail_out = cap - total;
        BrotliDecoderResult r = BrotliDecoderDecompressStream(
            s, &avail_in, &next_in, &avail_out, &next_out, NULL);
        total = (size_t)(next_out - buf);

        if (r == BROTLI_DECODER_RESULT_SUCCESS) break;
        if (r == BROTLI_DECODER_RESULT_ERROR) {
            free(buf);
            BrotliDecoderDestroyInstance(s);
            return 0;
        }
        if (r == BROTLI_DECODER_RESULT_NEEDS_MORE_OUTPUT) {
            cap *= 2;
            buf = realloc(buf, cap);
            if (!buf) { BrotliDecoderDestroyInstance(s); return 0; }
            continue;
        }
        if (avail_in == 0) {
            free(buf);
            BrotliDecoderDestroyInstance(s);
            return 0;
        }
    }

    BrotliDecoderDestroyInstance(s);
    *out = buf;
    *out_len = total;
    return 1;
}
*/
import "C" //nolint:gocritic // cgo preamble must directly precede this import

//nolint:gocritic // dupImport false positive: paired with import "C" above
import (
	"errors"
	"unsafe"
)

var errEncode = errors.New("c reference brotli compression failed")
var errDecode = errors.New("c reference brotli decompression failed")

// Encode compresses input using the C reference streaming encoder.
func Encode(input []byte, quality, lgwin int, sizeHint uint) ([]byte, error) {
	var inPtr *C.uint8_t
	if len(input) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&input[0]))
	}

	var out *C.uint8_t
	var outLen C.size_t
	//nolint:gocritic // false positive on multi-line cgo call
	ok := C.crefCompress(inPtr, C.size_t(len(input)),
		C.int(quality), C.int(lgwin), C.uint32_t(sizeHint),
		&out, &outLen)
	if ok == 0 {
		return nil, errEncode
	}
	defer C.free(unsafe.Pointer(out))

	return C.GoBytes(unsafe.Pointer(out), C.int(outLen)), nil
}

// Decode decompresses brotli data using the C reference decoder.
func Decode(compressed []byte) ([]byte, error) {
	var inPtr *C.uint8_t
	if len(compressed) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&compressed[0]))
	}

	var out *C.uint8_t
	var outLen C.size_t
	//nolint:gocritic // false positive on cgo call
	ok := C.crefDecompress(inPtr, C.size_t(len(compressed)), &out, &outLen)
	if ok == 0 {
		return nil, errDecode
	}
	defer C.free(unsafe.Pointer(out))

	return C.GoBytes(unsafe.Pointer(out), C.int(outLen)), nil
}

// EncodeDict compresses input with a raw prefix compound dictionary using
// the C reference streaming encoder.
func EncodeDict(input, dict []byte, quality, lgwin int, sizeHint uint) ([]byte, error) {
	var inPtr *C.uint8_t
	if len(input) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&input[0]))
	}
	var dictPtr *C.uint8_t
	if len(dict) > 0 {
		dictPtr = (*C.uint8_t)(unsafe.Pointer(&dict[0]))
	}

	var out *C.uint8_t
	var outLen C.size_t
	//nolint:gocritic // false positive on multi-line cgo call
	ok := C.crefCompressDict(inPtr, C.size_t(len(input)),
		dictPtr, C.size_t(len(dict)),
		C.int(quality), C.int(lgwin), C.uint32_t(sizeHint),
		&out, &outLen)
	if ok == 0 {
		return nil, errEncode
	}
	defer C.free(unsafe.Pointer(out))

	return C.GoBytes(unsafe.Pointer(out), C.int(outLen)), nil
}

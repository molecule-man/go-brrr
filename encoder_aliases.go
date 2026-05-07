// Aliases that re-export internal/encoder symbols under the lowercase names
// the decoder and Writer use. This keeps existing decoder code (decode.go,
// decode_state.go, bitreader.go) and writer.go diff-free at the symbol
// level — the package boundary is hidden behind these one-line aliases.

package brrr

import "github.com/molecule-man/go-brrr/internal/encoder"

// Constants forwarded from internal/encoder.
const (
	huffmanMaxCodeLength            = encoder.HuffmanMaxCodeLength
	alphabetSizeInsertAndCopyLength = encoder.AlphabetSizeInsertAndCopyLength
	alphabetSizeCodeLengths         = encoder.AlphabetSizeCodeLengths
	alphabetSizeLiteral             = encoder.AlphabetSizeLiteral
	alphabetSizeBlockCount          = encoder.AlphabetSizeBlockCount
	numHistogramDistanceSymbols     = encoder.NumHistogramDistanceSymbols
	numTransforms                   = encoder.NumTransforms
	maxCompoundDicts                = encoder.MaxCompoundDicts
	numDistanceShortCodes           = encoder.NumDistanceShortCodes
	maxDistanceBits                 = encoder.MaxDistanceBits
	windowGap                       = encoder.WindowGap
	literalContextBits              = encoder.LiteralContextBits
	distanceContextBits             = encoder.DistanceContextBits
	huffmanMaxCodeLengthCodeLength  = encoder.HuffmanMaxCodeLengthCodeLength
	initialRepeatedCodeLength       = encoder.InitialRepeatedCodeLength
	repeatPreviousCodeLength        = encoder.RepeatPreviousCodeLength
	dictMinWordLength               = encoder.DictMinWordLength
	dictMaxWordLength               = encoder.DictMaxWordLength
	transformOmitFirst9             = encoder.TransformOmitFirst9
)

// Function/factory aliases, error variables, and read-only data tables.
var (
	buildHuffmanTable            = encoder.BuildHuffmanTable
	buildSimpleHuffmanTable      = encoder.BuildSimpleHuffmanTable
	buildCodeLengthsHuffmanTable = encoder.BuildCodeLengthsHuffmanTable
	transformDictionaryWord      = encoder.TransformDictionaryWord
	newCompressor                = encoder.NewCompressor

	errTooManyDicts  = encoder.ErrTooManyDicts
	errEmptyDict     = encoder.ErrEmptyDict
	errQualityTooLow = encoder.ErrQualityTooLow

	cmdLut               = encoder.CmdLut
	contextLookupTable   = encoder.ContextLookupTable
	dictData             = encoder.DictData
	dictOffsetsByLength  = encoder.DictOffsetsByLength
	dictSizeBitsByLength = encoder.DictSizeBitsByLength
	blockLengthNBits     = encoder.BlockLengthNBits
	blockLengthOffset    = encoder.BlockLengthOffset
	codeLengthCodeOrder  = encoder.CodeLengthCodeOrder
	transformCutOffs     = encoder.TransformCutOffs
	transformTriplets    = encoder.TransformTriplets
)

// Type aliases for cross-package types accessed by name.
type (
	huffmanCode   = encoder.HuffmanCode
	symbolList    = encoder.SymbolList
	cmdLutElement = encoder.CmdLutElement
	compressor    = encoder.Compressor
)

// PreparedDictionary is a compound dictionary chunk built once and shared
// across many Writers. See [WriterOptions.Dictionaries].
type PreparedDictionary = encoder.PreparedDictionary

// PrepareDictionary builds a compound dictionary chunk. Re-exported from the
// encoder package.
func PrepareDictionary(data []byte) (*PreparedDictionary, error) {
	return encoder.PrepareDictionary(data)
}

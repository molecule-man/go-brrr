// Public surface used by the root brrr package: the decoder needs huffman
// table primitives and codec tables; Writer at root needs the compressor
// interface and a few errors/constants. Internal symbols stay lowercase;
// this file exports them via type/var/const aliases.

package encoder

// Constants used by the decoder and Writer.
const (
	HuffmanMaxCodeLength            = huffmanMaxCodeLength
	AlphabetSizeInsertAndCopyLength = alphabetSizeInsertAndCopyLength
	AlphabetSizeCodeLengths         = alphabetSizeCodeLengths
	AlphabetSizeLiteral             = alphabetSizeLiteral
	AlphabetSizeBlockCount          = alphabetSizeBlockCount
	NumHistogramDistanceSymbols     = numHistogramDistanceSymbols
	NumTransforms                   = numTransforms
	MaxCompoundDicts                = maxCompoundDicts
	NumDistanceShortCodes           = numDistanceShortCodes
	MaxDistanceBits                 = maxDistanceBits
	WindowGap                       = windowGap
	LiteralContextBits              = literalContextBits
	DistanceContextBits             = distanceContextBits
	HuffmanMaxCodeLengthCodeLength  = huffmanMaxCodeLengthCodeLength
	InitialRepeatedCodeLength       = initialRepeatedCodeLength
	RepeatPreviousCodeLength        = repeatPreviousCodeLength
	DictMinWordLength               = dictMinWordLength
	DictMaxWordLength               = dictMaxWordLength
	TransformOmitFirst9             = transformOmitFirst9
)

// Build/lookup helpers and read-only data tables shared with the decoder.
var (
	BuildHuffmanTable            = buildHuffmanTable
	BuildSimpleHuffmanTable      = buildSimpleHuffmanTable
	BuildCodeLengthsHuffmanTable = buildCodeLengthsHuffmanTable
	TransformDictionaryWord      = transformDictionaryWord
	NewCompressor                = newCompressor

	CmdLut               = &cmdLut
	ContextLookupTable   = &contextLookupTable
	DictData             = dictData
	DictOffsetsByLength  = &dictOffsetsByLength
	DictSizeBitsByLength = &dictSizeBitsByLength
	BlockLengthNBits     = &blockLengthNBits
	BlockLengthOffset    = &blockLengthOffset
	CodeLengthCodeOrder  = &codeLengthCodeOrder
	TransformCutOffs     = &transformCutOffs
	TransformTriplets    = &transformTriplets

	ErrTooManyDicts  = errTooManyDicts
	ErrEmptyDict     = errEmptyDict
	ErrQualityTooLow = errQualityTooLow
)

// Compressor is the interface used to drive a brotli stream from the public
// Writer at the package boundary.
type Compressor = compressor

// HuffmanCode is a single entry in a Huffman lookup table, exposed to the
// decoder so it can read tables built by [BuildHuffmanTable].
type HuffmanCode = huffmanCode

// SymbolList is the symbol-index storage used during Huffman tree
// construction, exposed to the decoder for [BuildHuffmanTable].
type SymbolList = symbolList

// CmdLutElement is a single entry in the brotli command lookup table.
type CmdLutElement = cmdLutElement

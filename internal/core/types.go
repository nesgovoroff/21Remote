// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import "strings"

// Card types.
const (
	NoCard          = 0
	LineOutCard     = 1
	LineInCard      = 2
	AESEBUCard      = 4
	AESEBUCardISFC  = 5
	AESEBUCardIOSFC = 6
	GPIOCard        = 7
	GPIOCardSlave   = 8
	ADAT8           = 9
	ADAT16          = 10
	TDIF8           = 11
	TDIF16          = 12
	SDIemb          = 13
	MicEvenIns      = 16
	MicOddIns       = 17
	MicEvenACmp     = 20
	MicOddACmp      = 21
	MicEven         = 22
	MicOdd          = 23
	MADIOptElect    = 24
	DL5Mic          = 26
	DL5LineOut      = 27
	UnknownCard     = 255
)

var typeNames = map[int]string{
	NoCard: "NoCard", LineOutCard: "LineOutCard", LineInCard: "LineInCard", AESEBUCard: "AESEBUCard",
	AESEBUCardISFC: "AESEBUCardISFC", AESEBUCardIOSFC: "AESEBUCardIOSFC", GPIOCard: "GPIOCard",
	GPIOCardSlave: "GPIOCardSlave", ADAT8: "ADAT8", ADAT16: "ADAT16", TDIF8: "TDIF8", TDIF16: "TDIF16",
	SDIemb: "SDIemb", MicEvenIns: "MicEvenIns", MicOddIns: "MicOddIns", MicEvenACmp: "MicEvenACmp",
	MicOddACmp: "MicOddACmp", MicEven: "MicEven", MicOdd: "MicOdd", MADIOptElect: "MADIOptElect",
	DL5Mic: "DL5Mic", DL5LineOut: "DL5LineOut", UnknownCard: "UnknownCard",
}

// TypeName is the enum name used in snapshot files.
func TypeName(t int) string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return itoa(t)
}

// ParseCardType reads a card type name (case-insensitive).
func ParseCardType(s string) int {
	l := strings.ToLower(strings.TrimSpace(s))
	for t, n := range typeNames {
		if strings.ToLower(n) == l {
			return t
		}
	}
	return UnknownCard
}

// IsMicType reports microphone cards.
func IsMicType(t int) bool {
	switch t {
	case MicEven, MicOdd, MicEvenACmp, MicOddACmp, MicEvenIns, MicOddIns:
		return true
	}
	return false
}

func isMicEven(t int) bool { return t == MicEven || t == MicEvenACmp || t == MicEvenIns }

// Acronym is the short name of a card type.
func Acronym(t int) string {
	switch t {
	case NoCard:
		return "NOC"
	case LineOutCard, DL5LineOut:
		return "OUT"
	case LineInCard:
		return "IN"
	case AESEBUCard, AESEBUCardISFC, AESEBUCardIOSFC:
		return "AES"
	case GPIOCard, GPIOCardSlave:
		return "GPIO"
	case ADAT8, ADAT16:
		return "ADAT"
	case TDIF8, TDIF16:
		return "TDIF"
	case SDIemb:
		return "SDI"
	case MicEvenIns, MicOddIns, MicEvenACmp, MicOddACmp, MicEven, MicOdd, DL5Mic:
		return "MIC"
	case MADIOptElect:
		return "MADI"
	}
	return "GEN"
}

// Description is the long name of a card type.
func Description(t int) string {
	switch t {
	case NoCard:
		return "No Card"
	case LineOutCard:
		return "Line out Card"
	case LineInCard:
		return "Line in Card"
	case AESEBUCard:
		return "AES/EBU Card"
	case AESEBUCardISFC:
		return "AES/EBU Card with input SFC"
	case AESEBUCardIOSFC:
		return "AES/EBU Card with input and output SFC"
	case GPIOCard:
		return "GPIO Card"
	case GPIOCardSlave:
		return "GPIO Card with slave Card"
	case ADAT8, ADAT16:
		return "ADAT Card"
	case TDIF8, TDIF16:
		return "TDIF Card"
	case SDIemb:
		return "SDI embedder/deembedder Card"
	case MicEvenIns:
		return "Microphone Card in even slot, with insert"
	case MicOddIns:
		return "Microphone Card in odd slot with insert"
	case MicEvenACmp:
		return "Microphone Card in even slot with analog compressor"
	case MicOddACmp:
		return "Microphone Card in odd slot with analog compressor"
	case MicEven:
		return "Microphone Card in even slot"
	case MicOdd:
		return "Microphone Card in odd slot"
	case MADIOptElect:
		return "MADI Card optical and electrical"
	case DL5Mic:
		return "Microphone Card for DL5"
	case DL5LineOut:
		return "Line out Card for DL5"
	}
	return "Unknown"
}

// Strip kinds (one per channel-view component class).
const (
	KindMic     = "mic"
	KindDL5     = "dl5"
	KindAES     = "aes"
	KindADAT    = "adat"
	KindGPIO    = "gpio"
	KindLineIn  = "linein"
	KindLineOut = "lineout"
	KindGeneric = "generic"
)

// stripLayout gives the strip kind and channel count of a card type.
func stripLayout(t int) (kind string, channels int) {
	switch t {
	case LineOutCard, DL5LineOut:
		return KindLineOut, 1
	case LineInCard:
		return KindLineIn, 1
	case AESEBUCard, AESEBUCardISFC, AESEBUCardIOSFC:
		return KindAES, 1
	case GPIOCard, GPIOCardSlave:
		return KindGPIO, 1
	case ADAT8, ADAT16:
		return KindADAT, 1
	case MicEvenIns, MicOddIns, MicEvenACmp, MicOddACmp, MicEven, MicOdd:
		return KindMic, 4
	case DL5Mic:
		return KindDL5, 8
	}
	return KindGeneric, 1
}

// Default strip colours (ARGB) from each component's InitializeComponent.
var kindColor = map[string][3]int{
	KindMic: {0xaa, 56, 15}, KindDL5: {0xbe, 80, 0}, KindAES: {0, 100, 0xb4}, KindADAT: {0x96, 120, 0},
	KindGPIO: {80, 0xa0, 0}, KindLineIn: {0, 100, 0}, KindLineOut: {0, 100, 0}, KindGeneric: {100, 100, 100},
}

// hardware view: slot width (in slots) and image per card type (HardwareComponent.InitCardTable).
func slotWidth(t int) int {
	switch t {
	case GPIOCard, AESEBUCard, AESEBUCardISFC, AESEBUCardIOSFC, TDIF8, TDIF16:
		return 2
	}
	return 1
}

func slotImage(t int) string {
	switch t {
	case LineOutCard:
		return "lineout"
	case LineInCard:
		return "linein"
	case GPIOCard:
		return "gpio"
	case MicEven, MicOdd, MicEvenACmp, MicOddACmp, MicEvenIns, MicOddIns:
		return "mic"
	case DL5Mic:
		return "dl5mic"
	case AESEBUCard, AESEBUCardISFC, AESEBUCardIOSFC:
		return "aesebu"
	case TDIF8, TDIF16:
		return "tdif"
	case ADAT8, ADAT16:
		return "adat"
	}
	return "unknown"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

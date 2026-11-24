package nodecli

import (
	"math"
	"strconv"
	"strings"
)

// ftoa and ftoa3 print floats exactly as MeshCore's console does, so FakeNode's read-backs are the real ones
// (869.618 reads back as "869.6179809"). Ported from MeshCore src/helpers/TxtDataHelpers.cpp:57-159 at
// repeater-v1.18.0 (MIT, Copyright (c) 2025 Scott Powell / rippleradios.com), unchanged since v1.11.0.

// ftoa is StrHelper::ftoa: the float32's digits, 7 decimals cut (not rounded), trailing zeros dropped but one
// decimal kept; "0" when the value is out of its range.
func ftoa(f float32) string {
	if f == 0 {
		return "0.0"
	}
	bits := int32(math.Float32bits(f))
	exp2 := int(uint8(bits>>23)) - 127
	mantissa := bits&0xFFFFFF | 0x800000
	var intPart, fracPart int32
	switch {
	case exp2 >= 31, exp2 < -23:
		return "0" // _FTOA_TOO_LARGE or _FTOA_TOO_SMALL: the caller's fallback
	case exp2 >= 23:
		intPart = mantissa << (exp2 - 23)
	case exp2 >= 0:
		intPart = mantissa >> (23 - exp2)
		fracPart = (mantissa << (exp2 + 1)) & 0xFFFFFF
	default:
		fracPart = (mantissa & 0xFFFFFF) >> -(exp2 + 1)
	}
	var b strings.Builder
	if bits < 0 {
		b.WriteByte('-')
	}
	b.WriteString(strconv.Itoa(int(intPart)))
	b.WriteByte('.')
	if fracPart == 0 {
		b.WriteByte('0')
		return b.String()
	}
	var digits [7]byte
	for m := range digits {
		fracPart = fracPart<<3 + fracPart<<1
		digits[m] = byte(fracPart>>24) + '0'
		fracPart &= 0xFFFFFF
	}
	d := strings.TrimRight(string(digits[:]), "0")
	if d == "" {
		d = "0"
	}
	b.WriteString(d)
	return b.String()
}

// ftoa3 is StrHelper::ftoa3: rounded to 3 decimals, trailing zeros and a bare point dropped ("250", "62.5").
func ftoa3(f float32) string {
	half := float32(0.5)
	if f < 0 {
		half = -0.5
	}
	v := int32(float32(f*1000) + half) // explicit float32 rounding, as the C does, and no fused multiply-add
	w, d := v/1000, v%1000
	if d < 0 {
		d = -d
	}
	s := strconv.Itoa(int(w)) + "." + leftPad3(int(d))
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// shortestPrinted is the shortest decimal the node prints as text (which is x): the node prints a location
// through float32 cut to 7 decimals, so 51.5074 reads back as "51.5074005" and -0.1278 as "-0.1277999", and this
// turns them back into what was typed.
func shortestPrinted(x float64, text string) float64 {
	scale := 1.0
	for k := 0; k <= 7; k++ {
		c := math.Round(x*scale) / scale
		if ftoa(float32(c)) == text {
			return c
		}
		scale *= 10
	}
	return x
}

func leftPad3(d int) string {
	s := strconv.Itoa(d)
	return strings.Repeat("0", 3-len(s)) + s
}

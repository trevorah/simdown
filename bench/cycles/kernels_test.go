package main

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestSWARMatchesScalar(t *testing.T) {
	b := make([]byte, 100003)
	for i := range b {
		b[i] = byte(rand.IntN(256))
	}
	n := len(b)/64 + 1
	want := scalarBitmap(b)
	wantMap := slices.Clone(bitmap[:n])
	if got := swarBitmap(b); got != want || !slices.Equal(bitmap[:n], wantMap) {
		t.Fatalf("swar bitmap differs from scalar (%d vs %d bits)", got, want)
	}
}

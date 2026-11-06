package vec

import (
	"math"
	"reflect"
	"testing"
)

func assertNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > tolerance {
		t.Fatalf("got %g, want finite value within %g of %g", got, tolerance, want)
	}
}

func TestCosine(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"empty", nil, nil, 0},
		{"mismatch", []float32{1}, []float32{1, 2}, 0},
		{"zero", []float32{0, 0}, []float32{1, 2}, 0},
		{"parallel", []float32{3, 4}, []float32{6, 8}, 1},
		{"opposite", []float32{3, 4}, []float32{-3, -4}, -1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"angle", []float32{1, 0}, []float32{1, 1}, 1 / math.Sqrt2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := append([]float32(nil), tc.a...), append([]float32(nil), tc.b...)
			assertNear(t, Cosine(tc.a, tc.b), tc.want, 1e-7)
			if !reflect.DeepEqual(a, tc.a) || !reflect.DeepEqual(b, tc.b) {
				t.Fatal("Cosine mutated its inputs")
			}
		})
	}
}

func TestNormalizeInPlace(t *testing.T) {
	v := []float32{3, -4}
	got := Normalize(v)
	if &got[0] != &v[0] {
		t.Fatalf("Normalize = %v, input = %v", got, v)
	}
	assertNear(t, float64(v[0]), .6, 1e-6)
	assertNear(t, float64(v[1]), -.8, 1e-6)
	before := append([]float32(nil), v...)
	Normalize(v)
	for i := range v {
		assertNear(t, float64(v[i]), float64(before[i]), 1e-6)
	}
	if Normalize(nil) != nil {
		t.Fatal("nil became non-nil")
	}
	zero := []float32{0, 0}
	if got := Normalize(zero); !reflect.DeepEqual(got, []float32{0, 0}) {
		t.Fatalf("zero vector = %v", got)
	}
}

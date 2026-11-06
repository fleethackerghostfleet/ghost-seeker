package vec

import (
	"math"
	"reflect"
	"testing"
)

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
			if got := Cosine(tc.a, tc.b); math.Abs(got-tc.want) > 1e-7 {
				t.Fatalf("Cosine = %g, want %g", got, tc.want)
			}
			if !reflect.DeepEqual(a, tc.a) || !reflect.DeepEqual(b, tc.b) {
				t.Fatal("Cosine mutated its inputs")
			}
		})
	}
}

func TestNormalizeInPlace(t *testing.T) {
	v := []float32{3, -4}
	got := Normalize(v)
	if &got[0] != &v[0] || math.Abs(float64(v[0])-.6) > 1e-6 || math.Abs(float64(v[1])+.8) > 1e-6 {
		t.Fatalf("Normalize = %v, input = %v", got, v)
	}
	before := append([]float32(nil), v...)
	Normalize(v)
	for i := range v {
		if math.Abs(float64(v[i]-before[i])) > 1e-6 {
			t.Fatalf("normalizing unit vector changed it: %v", v)
		}
	}
	if Normalize(nil) != nil {
		t.Fatal("nil became non-nil")
	}
	zero := []float32{0, 0}
	if got := Normalize(zero); !reflect.DeepEqual(got, []float32{0, 0}) {
		t.Fatalf("zero vector = %v", got)
	}
}
